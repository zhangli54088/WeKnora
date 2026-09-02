import assert from 'node:assert/strict'
import test from 'node:test'

import type { UserKnowledgeState } from '@/api/memory'
import type { WikiGraphData } from '@/api/wiki'
import {
  filterPersonalKnowledgeGraph,
  mergePersonalKnowledgeGraph,
  summarizeKnowledgeStates,
} from './personalLearningGraph'

const graph: WikiGraphData = {
  nodes: [
    { id: 'p1', knowledge_base_id: 'kb', slug: 'a', title: 'Transformer', page_type: 'concept', link_count: 1 },
    { id: 'p2', knowledge_base_id: 'kb', slug: 'b', title: 'Mamba', page_type: 'concept', link_count: 2 },
    { id: 'p3', knowledge_base_id: 'kb', slug: 'c', title: 'CNN', page_type: 'concept', link_count: 1 },
  ],
  edges: [{ source: 'a', target: 'b' }, { source: 'b', target: 'c' }],
  meta: { mode: 'overview', total: 3, returned: 3, truncated: false },
}

function state(id: string, status: UserKnowledgeState['status'], evidenceCount = 1): UserKnowledgeState {
  return {
    id: `state-${id}`, wiki_page_id: id, knowledge_base_id: 'kb', title: id, slug: id,
    status, confidence: 0.7, evidence_count: evidenceCount,
    last_evidence_at: '2026-09-01T00:00:00Z', updated_at: '2026-09-01T00:00:00Z',
  }
}

test('merges state by wiki page id and defaults missing nodes to unknown', () => {
  const nodes = mergePersonalKnowledgeGraph(graph, [state('p1', 'exposed'), state('p2', 'familiar')])
  assert.deepEqual(nodes.map(node => node.learning_status), ['exposed', 'familiar', 'unknown'])
})

test('supports status and title filters', () => {
  const nodes = mergePersonalKnowledgeGraph(graph, [state('p1', 'exposed'), state('p2', 'mastered')])
  assert.deepEqual(
    [...filterPersonalKnowledgeGraph(nodes, graph.edges, { query: 'mamba', status: 'mastered', litOnly: false, includeContext: false })],
    ['b'],
  )
})

test('lit-only context mode keeps one-hop unknown neighbors', () => {
  const nodes = mergePersonalKnowledgeGraph(graph, [state('p2', 'familiar', 2)])
  assert.deepEqual(
    [...filterPersonalKnowledgeGraph(nodes, graph.edges, { query: '', status: 'all', litOnly: true, includeContext: true })].sort(),
    ['a', 'b', 'c'],
  )
  assert.deepEqual(summarizeKnowledgeStates(nodes), { lit: 1, exposed: 0, familiar: 1, mastered: 0, evidence: 2 })
})
