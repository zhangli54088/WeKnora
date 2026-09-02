import type { UserKnowledgeState, KnowledgeStatus } from '@/api/memory'
import type { WikiGraphData } from '@/api/wiki'

export type PersonalWikiNode = WikiGraphData['nodes'][number] & {
  learning_status: KnowledgeStatus
  knowledge_state?: UserKnowledgeState
}

export interface PersonalGraphFilters {
  query: string
  status: KnowledgeStatus | 'all'
  litOnly: boolean
  includeContext: boolean
}

export function mergePersonalKnowledgeGraph(
  graph: WikiGraphData,
  states: UserKnowledgeState[],
): PersonalWikiNode[] {
  const stateByPageID = new Map(states.map(state => [state.wiki_page_id, state]))
  return graph.nodes.map(node => {
    const state = stateByPageID.get(node.id)
    return {
      ...node,
      learning_status: state?.status || 'unknown',
      knowledge_state: state,
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
