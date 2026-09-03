import type { UserKnowledgeState, KnowledgeStatus, LearningRecommendation } from '@/api/memory'
import type { WikiGraphData } from '@/api/wiki'

export type PersonalWikiNode = WikiGraphData['nodes'][number] & {
  learning_status: KnowledgeStatus
  knowledge_state?: UserKnowledgeState
  recommendation?: LearningRecommendation
}

export interface PersonalGraphFilters {
  query: string
  status: KnowledgeStatus | 'all'
  litOnly: boolean
  includeContext: boolean
  recommendedOnly?: boolean
}

export function mergePersonalKnowledgeGraph(
  graph: WikiGraphData,
  states: UserKnowledgeState[],
  recommendations: LearningRecommendation[] = [],
): PersonalWikiNode[] {
  const stateByPageID = new Map(states.map(state => [state.wiki_page_id, state]))
  const recommendationByID = new Map(recommendations.map(item => [item.wiki_page_id, item]))
  return graph.nodes.map(node => {
    const state = stateByPageID.get(node.id)
    const recommendation = recommendationByID.get(node.id)
    return {
      ...node,
      learning_status: state?.status || 'unknown',
      knowledge_state: state,
      // A stale response must never re-label a now-exposed node as unknown.
      recommendation: !state && recommendation?.knowledge_base_id === node.knowledge_base_id
        ? recommendation : undefined,
    }
  })
}

export function filterPersonalKnowledgeGraph(
  nodes: PersonalWikiNode[],
  edges: WikiGraphData['edges'],
  filters: PersonalGraphFilters,
): Set<string> {
  const query = filters.query.trim().toLocaleLowerCase()
  const directlyVisible = new Set<string>()
  if (filters.recommendedOnly) {
    const byID = new Map(nodes.map(node => [node.id, node.slug]))
    for (const node of nodes) {
      if (!node.recommendation || (query && !node.title.toLocaleLowerCase().includes(query))) continue
      if (filters.status !== 'all' && filters.status !== 'unknown') continue
      directlyVisible.add(node.slug)
      // Recommended-only takes precedence over lit-only and keeps the actual
      // supporting paths, not isolated unknown circles.
      for (const support of node.recommendation.supporting_nodes) {
        for (const id of support.path) {
          const slug = byID.get(id)
          if (slug) directlyVisible.add(slug)
        }
      }
    }
    return directlyVisible
  }
  for (const node of nodes) {
    if (query && !node.title.toLocaleLowerCase().includes(query)) continue
    if (filters.status !== 'all' && node.learning_status !== filters.status) continue
    if (filters.litOnly && node.learning_status === 'unknown') continue
    directlyVisible.add(node.slug)
  }
  if (!filters.includeContext || !filters.litOnly) return directlyVisible

  const visible = new Set(directlyVisible)
  for (const edge of edges) {
    if (directlyVisible.has(edge.source)) visible.add(edge.target)
    if (directlyVisible.has(edge.target)) visible.add(edge.source)
  }
  return visible
}

export function summarizeKnowledgeStates(nodes: PersonalWikiNode[]) {
  const counts = { lit: 0, exposed: 0, familiar: 0, mastered: 0, evidence: 0 }
  for (const node of nodes) {
    if (node.learning_status !== 'unknown') counts.lit += 1
    if (node.learning_status === 'exposed') counts.exposed += 1
    if (node.learning_status === 'familiar') counts.familiar += 1
    if (node.learning_status === 'mastered') counts.mastered += 1
    counts.evidence += node.knowledge_state?.evidence_count || 0
  }
  return counts
}
