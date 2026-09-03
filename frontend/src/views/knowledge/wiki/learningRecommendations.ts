import type { LearningRecommendation, LearningRecommendationView } from '@/api/memory'
import type { WikiGraphData } from '@/api/wiki'

export const recommendationReasonKeys: Record<string, string> = {
  adjacent_to_familiar: 'learningRecommendation.adjacentFamiliar',
  adjacent_to_mastered: 'learningRecommendation.adjacentMastered',
  adjacent_to_exposed: 'learningRecommendation.adjacentExposed',
  two_hop_connection: 'learningRecommendation.twoHop',
  multiple_supporting_anchors: 'learningRecommendation.multipleAnchors',
  supported_by_long_term_memory: 'learningRecommendation.memorySupport',
  recent_learning_context: 'learningRecommendation.recentContext',
}

export function recommendationReasonLabels(item: LearningRecommendation, translate: (key: string) => string): string[] {
  return item.reason_codes.flatMap(code => recommendationReasonKeys[code] ? [translate(recommendationReasonKeys[code])] : [])
}

/** Merge only server-provided real Wiki nodes/edges; never infer new relationships. */
export function mergeRecommendationGraph(graph: WikiGraphData, view: LearningRecommendationView | null, kbID: string): WikiGraphData {
  if (!view || view.knowledge_base_id !== kbID) return graph
  const nodes = new Map(graph.nodes.map(node => [node.id, node]))
  for (const node of view.context_graph.nodes) {
    if (node.knowledge_base_id === kbID && !nodes.has(node.id)) nodes.set(node.id, node)
  }
  const slugs = new Set([...nodes.values()].filter(node => node.knowledge_base_id === kbID).map(node => node.slug))
  const edges = new Map(graph.edges.map(edge => [JSON.stringify([edge.source, edge.target]), edge]))
  for (const edge of view.context_graph.edges) {
    if (slugs.has(edge.source) && slugs.has(edge.target)) edges.set(JSON.stringify([edge.source, edge.target]), edge)
  }
  return { ...graph, nodes: [...nodes.values()], edges: [...edges.values()] }
}

/** Explicit allow-list: debug never dumps arbitrary API metadata or requests. */
export function recommendationDebugEntries(item: LearningRecommendation, view: LearningRecommendationView | null): [string, string][] {
  return [
    ['recommendation_score', String(item.score)], ['rank', String(item.rank)], ['hop', String(item.hop)],
    ...Object.entries(item.score_components).filter(([key]) => ['structural', 'anchor_strength', 'multi_anchor', 'recency', 'long_term_memory'].includes(key)).map(([key, value]): [string, string] => [key, String(value)]),
    ['supporting_node_ids', item.supporting_nodes.map(node => node.wiki_page_id).join(', ')],
    ['traversed_paths', item.supporting_nodes.map(node => node.path.join(' → ')).join('; ')],
    ['knowledge_base_id', item.knowledge_base_id], ['generated_at', view?.generated_at || '-'],
    ['scoring_at', view?.scoring_at || '-'],
  ]
}

// Versioned, dependency-injected loading keeps recommendation failures and stale
// responses independent of the graph/state loaders. Also directly testable.
export function createRecommendationLoader(fetch: (kbID: string) => Promise<LearningRecommendationView>) {
  let version = 0
  return {
    invalidate() { version += 1 },
    async load(kbID: string, commit: (view: LearningRecommendationView | null, failed: boolean) => void) {
      const request = ++version
      try {
        const view = await fetch(kbID)
        if (request === version && view.knowledge_base_id === kbID) commit(view, false)
      } catch {
        if (request === version) commit(null, true)
      }
    },
  }
}

export async function selectLearningRecommendation(item: LearningRecommendation, selectSlug: (slug: string) => void | Promise<void>) {
  await selectSlug(item.slug)
}
