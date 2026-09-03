package memory

import (
	"math"
	"sort"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// Sum = 1. A weakest one-hop candidate scores >= .71; even a strongest
	// two-hop candidate scores <= .6425. Recency/memory cannot override distance.
	recommendationStructuralWeight = 0.65
	recommendationAnchorWeight = 0.20
	recommendationMultiAnchorWeight = 0.05
	recommendationRecencyWeight = 0.05
	recommendationMemoryWeight = 0.05
	recommendationMasteredStrength = 1.0
	recommendationFamiliarStrength = 0.8
	recommendationExposedStrength = 0.3
	recommendationOneHopProximity = 1.0
	recommendationTwoHopProximity = 0.45
	recommendationMultiAnchorCap = 3
	recommendationRecencyDays = 30.0
	recommendationMaxEdges = 20000
	recommendationMaxVisits = 100000
	recommendationMaxHops = 2
)

// LearningGapCandidate is deliberately internal: unknown does not imply a
// definite knowledge gap. A candidate needs a real bounded path to an anchor.
type learningGapCandidate struct {
	page *types.WikiPage
	hop int
	support map[string][]string
}

type recommendationGraph struct {
	pages map[string]*types.WikiPage
	ids []string
	neighbors map[string][]string
	edges []types.WikiGraphEdge
	truncated bool
}

func buildRecommendationGraph(pages []*types.WikiPage) recommendationGraph {
	g := recommendationGraph{pages: map[string]*types.WikiPage{}, neighbors: map[string][]string{}, edges: []types.WikiGraphEdge{}}
	bySlug := map[string]string{}
	for _, p := range pages { if p != nil { g.pages[p.ID] = p; bySlug[p.Slug] = p.ID } }
	for id := range g.pages { g.ids = append(g.ids, id) }
	sort.Strings(g.ids)
	neighbors := map[string]map[string]bool{}
	for _, id := range g.ids { neighbors[id] = map[string]bool{} }
	edgeCount := 0
	for _, id := range g.ids {
		p := g.pages[id]
		links := append([]string(nil), p.OutLinks...)
		sort.Strings(links)
		seen := map[string]bool{}
		for _, slug := range links {
			target, ok := bySlug[slug]
			if !ok || target == id || seen[target] { continue }
			seen[target] = true
			if edgeCount >= recommendationMaxEdges { g.truncated = true; break }
			edgeCount++
			neighbors[id][target] = true
			neighbors[target][id] = true
			g.edges = append(g.edges, types.WikiGraphEdge{Source: p.Slug, Target: slug})
		}
		if g.truncated { break }
	}
	for _, id := range g.ids {
		for neighbor := range neighbors[id] { g.neighbors[id] = append(g.neighbors[id], neighbor) }
		sort.Strings(g.neighbors[id])
	}
	return g
}

func recommendationAnchorStrength(status string) float64 {
	switch status {
	case types.UserKnowledgeStatusMastered: return recommendationMasteredStrength
	case types.UserKnowledgeStatusFamiliar: return recommendationFamiliarStrength
	case types.UserKnowledgeStatusExposed: return recommendationExposedStrength
	default: return 0
	}
}

func generateLearningCandidates(g recommendationGraph, states map[string]*types.UserKnowledgeState, limit int) ([]learningGapCandidate, bool) {
	anchors := []string{}
	for _, id := range g.ids {
		if state := states[id]; state != nil && state.EvidenceCount > 0 && recommendationAnchorStrength(state.Status) > 0 {
			anchors = append(anchors, id)
		}
	}
	candidates := map[string]*learningGapCandidate{}
	visits := 0
	truncated := g.truncated
	add := func(anchor, target string, path []string) {
		// Existing known states are never mixed into the unknown recommendation pool.
		if state := states[target]; state != nil && state.EvidenceCount > 0 { return }
		hop := len(path)-1
		c := candidates[target]
		if c == nil { c = &learningGapCandidate{page: g.pages[target], hop: hop, support: map[string][]string{}}; candidates[target] = c }
		if hop != c.hop { return }
		if _, exists := c.support[anchor]; !exists { c.support[anchor] = path }
	}
	for _, anchor := range anchors {
		for _, target := range g.neighbors[anchor] {
			visits++
			if visits > recommendationMaxVisits { truncated = true; break }
			add(anchor, target, []string{anchor, target})
		}
		if visits > recommendationMaxVisits { break }
	}
	// Complete one-hop support first. Expand only if it cannot fill the response.
	if len(candidates) < limit && recommendationMaxHops >= 2 && visits <= recommendationMaxVisits {
	outer:
		for _, anchor := range anchors {
			for _, via := range g.neighbors[anchor] {
				for _, target := range g.neighbors[via] {
					visits++
					if visits > recommendationMaxVisits { truncated = true; break outer }
					if target == anchor { continue }
					add(anchor, target, []string{anchor, via, target})
				}
			}
		}
	}
	out := make([]learningGapCandidate, 0, len(candidates))
	for _, id := range g.ids { if c := candidates[id]; c != nil { out = append(out, *c) } }
	return out, truncated
}

func scoreLearningCandidate(components types.RecommendationScoreComponents) float64 {
	return normalizeEvidenceWeight(
		recommendationStructuralWeight*normalizeEvidenceWeight(components.Structural) +
		recommendationAnchorWeight*normalizeEvidenceWeight(components.AnchorStrength) +
		recommendationMultiAnchorWeight*normalizeEvidenceWeight(components.MultiAnchor) +
		recommendationRecencyWeight*normalizeEvidenceWeight(components.Recency) +
		recommendationMemoryWeight*normalizeEvidenceWeight(components.LongTermMemory))
}

func recommendationRecency(last, now time.Time) float64 {
	if last.IsZero() { return 0 }
	days := math.Max(0, now.Sub(last).Hours()/24)
	return math.Exp(-days/recommendationRecencyDays)
}

func rankLearningCandidates(candidates []learningGapCandidate, g recommendationGraph, states map[string]*types.UserKnowledgeState, memory map[string]bool, now time.Time) []types.LearningRecommendation {
	out := make([]types.LearningRecommendation, 0, len(candidates))
	for _, c := range candidates {
		components := types.RecommendationScoreComponents{Structural: recommendationOneHopProximity}
		if c.hop == 2 { components.Structural = recommendationTwoHopProximity }
		components.MultiAnchor = math.Min(1, float64(len(c.support)-1)/float64(recommendationMultiAnchorCap-1))
		support := []types.SupportingKnowledgeNode{}
		reasons := map[string]bool{}
		for _, id := range g.ids {
			path, ok := c.support[id]; if !ok { continue }
			state := states[id]
			components.AnchorStrength = math.Max(components.AnchorStrength, recommendationAnchorStrength(state.Status))
			components.Recency = math.Max(components.Recency, recommendationRecency(state.LastEvidenceAt, now))
			if memory[id] { components.LongTermMemory = 1 }
			if c.hop == 1 { reasons["adjacent_to_"+state.Status] = true } else { reasons["two_hop_connection"] = true }
			support = append(support, types.SupportingKnowledgeNode{
				WikiPageID: id, Title: g.pages[id].Title, Slug: g.pages[id].Slug, Status: state.Status,
				EvidenceCount: state.EvidenceCount, LastEvidenceAt: state.LastEvidenceAt, MemorySupported: memory[id], Path: path,
			})
		}
		if len(support) > 1 { reasons["multiple_supporting_anchors"] = true }
		if components.LongTermMemory > 0 { reasons["supported_by_long_term_memory"] = true }
		if components.Recency >= math.Exp(-1) { reasons["recent_learning_context"] = true }
		codes := []string{}
		for code := range reasons { codes = append(codes, code) }; sort.Strings(codes)
		out = append(out, types.LearningRecommendation{
			WikiPageID: c.page.ID, KnowledgeBaseID: c.page.KnowledgeBaseID, Slug: c.page.Slug, Title: c.page.Title,
			Status: "unknown", Hop: c.hop, Score: scoreLearningCandidate(components), ScoreComponents: components,
			SupportingNodes: support, ReasonCodes: codes,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score { return out[i].Score > out[j].Score }
		if out[i].Hop != out[j].Hop { return out[i].Hop < out[j].Hop }
		if out[i].Title != out[j].Title { return out[i].Title < out[j].Title }
		return out[i].WikiPageID < out[j].WikiPageID
	})
	for i := range out { out[i].Rank = i+1 }
	return out
}

func recommendationContextGraph(g recommendationGraph, recommendations []types.LearningRecommendation) types.WikiGraphData {
	keep := map[string]bool{}
	for _, r := range recommendations {
		keep[r.WikiPageID] = true
		for _, s := range r.SupportingNodes { for _, id := range s.Path { keep[id] = true } }
	}
	result := types.WikiGraphData{Nodes: []types.WikiGraphNode{}, Edges: []types.WikiGraphEdge{}}
	slugs := map[string]bool{}
	for _, id := range g.ids {
		if !keep[id] { continue }; p := g.pages[id]; slugs[p.Slug] = true
		result.Nodes = append(result.Nodes, types.WikiGraphNode{ID: id, KnowledgeBaseID: p.KnowledgeBaseID, Slug: p.Slug, Title: p.Title, PageType: p.PageType, LinkCount: len(g.neighbors[id])})
	}
	for _, edge := range g.edges { if slugs[edge.Source] && slugs[edge.Target] { result.Edges = append(result.Edges, edge) } }
	result.Meta = types.WikiGraphMeta{Mode: types.WikiGraphModeOverview, Total: len(g.ids), Returned: len(result.Nodes), Truncated: len(result.Nodes)<len(g.ids)}
	return result
}
