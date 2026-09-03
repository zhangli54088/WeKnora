import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { parse, compileScript } from '@vue/compiler-sfc'
import { createSSRApp, defineComponent, h } from 'vue'
import { renderToString } from '@vue/server-renderer'
import { createI18n } from 'vue-i18n'
import ts from 'typescript'
import en from '../../../i18n/locales/en-US'
import type { LearningRecommendation, LearningRecommendationView, UserKnowledgeState } from '@/api/memory'
import type { WikiGraphData } from '@/api/wiki'
import { filterPersonalKnowledgeGraph, mergePersonalKnowledgeGraph } from './personalLearningGraph'
import {
  createRecommendationLoader, mergeRecommendationGraph, recommendationDebugEntries,
  recommendationReasonLabels, selectLearningRecommendation,
} from './learningRecommendations'

const graph: WikiGraphData = {
  nodes: [
    { id: 'a', knowledge_base_id: 'kb', slug: 'anchor', title: 'Anchor', page_type: 'concept', link_count: 1 },
    { id: 'b', knowledge_base_id: 'kb', slug: 'candidate', title: 'Candidate', page_type: 'concept', link_count: 1 },
    { id: 'c', knowledge_base_id: 'kb', slug: 'other', title: 'Other', page_type: 'concept', link_count: 0 },
  ], edges: [{ source: 'anchor', target: 'candidate' }],
  meta: { mode: 'overview', total: 3, returned: 3, truncated: false },
}
const item: LearningRecommendation = {
  wiki_page_id: 'b', knowledge_base_id: 'kb', slug: 'candidate', title: 'Candidate', status: 'unknown', score: 0.85, rank: 1, hop: 1,
  reason_codes: ['adjacent_to_familiar', 'supported_by_long_term_memory'],
  score_components: { structural: 1, anchor_strength: 0.8, multi_anchor: 0, recency: 0, long_term_memory: 1 },
  supporting_nodes: [{ wiki_page_id: 'a', slug: 'anchor', title: 'Anchor', status: 'familiar', evidence_count: 1, last_evidence_at: '2026-09-03T00:00:00Z', memory_supported: true, path: ['a', 'b'] }],
}
const view: LearningRecommendationView = {
  knowledge_base_id: 'kb', generated_at: '2026-09-03T00:00:00Z', scoring_at: '2026-09-03T00:00:00Z',
  wiki_enabled: true, truncated: false, recommendations: [item], context_graph: graph,
}
const anchorState: UserKnowledgeState = {
  id: 'state', wiki_page_id: 'a', knowledge_base_id: 'kb', title: 'Anchor', slug: 'anchor', status: 'familiar', confidence: 1, evidence_count: 1,
  last_evidence_at: '2026-09-03T00:00:00Z', updated_at: '2026-09-03T00:00:00Z',
}

test('recommendation overlay merges by wiki_page_id, not slug/title, and does not mutate state', () => {
  const nodes = mergePersonalKnowledgeGraph(graph, [anchorState], [{ ...item, title: 'different title', slug: 'different-slug' }])
  assert.equal(nodes[1].recommendation?.rank, 1)
  assert.equal(nodes[1].learning_status, 'unknown')
  assert.equal(nodes[0].learning_status, 'familiar')
  assert.equal(nodes[0].recommendation, undefined)
  assert.equal(mergePersonalKnowledgeGraph(graph, [], [{ ...item, knowledge_base_id: 'foreign' }])[1].recommendation, undefined)
})

test('recommended-only keeps supporting anchors and paths, ignoring conflicting lit-only', () => {
  const nodes = mergePersonalKnowledgeGraph(graph, [anchorState], [item])
  const visible = filterPersonalKnowledgeGraph(nodes, graph.edges, { query: '', status: 'all', litOnly: true, includeContext: false, recommendedOnly: true })
  assert.deepEqual([...visible].sort(), ['anchor', 'candidate'])
})

test('unknown to exposed refresh removes even a stale recommendation overlay', () => {
  const exposed: UserKnowledgeState = { ...anchorState, wiki_page_id: 'b', status: 'exposed' }
  const nodes = mergePersonalKnowledgeGraph(graph, [anchorState, exposed], [item])
  assert.equal(nodes[1].learning_status, 'exposed')
  assert.equal(nodes[1].recommendation, undefined)
})

test('context graph adds actual supporting nodes outside overview without altering input', () => {
  const overview = { ...graph, nodes: [graph.nodes[2]], edges: [] }
  assert.equal(mergeRecommendationGraph(overview, view, 'kb').nodes.length, 3)
  assert.equal(overview.nodes.length, 1)
  assert.strictEqual(mergeRecommendationGraph(overview, view, 'foreign'), overview)
})

test('selecting a recommendation invokes the existing graph node/drawer navigation', async () => {
  let selected = ''
  await selectLearningRecommendation(item, slug => { selected = slug })
  assert.equal(selected, 'candidate')
  const source = readFileSync(new URL('./LearningRecommendationPanel.vue', import.meta.url), 'utf8')
  assert.ok(source.includes(`@click="$emit('select', item)"`))
})

test('reasons use i18n and debug is an explicit safe field list', () => {
  const labels = recommendationReasonLabels(item, key => key.split('.').reduce<any>((v, part) => v[part], en))
  assert.match(labels[0], /Directly linked/)
  assert.match(labels[1], /long-term memory/)
  const hostile = { ...item, Authorization: 'secret', prompt: 'private', score_components: { ...item.score_components, Cookie: 'secret', JWT: 'secret' } }
  const debug = JSON.stringify(recommendationDebugEntries(hostile, view))
  assert.ok(debug.includes('structural'))
  assert.ok(debug.includes('supporting_node_ids'))
  for (const sensitive of ['Authorization', 'Cookie', 'JWT', 'private', 'secret']) assert.ok(!debug.includes(sensitive))
})

test('recommendation errors do not change the graph and clear stale recommendations', async () => {
  const before = JSON.stringify(graph)
  const loader = createRecommendationLoader(async () => { throw new Error('offline') })
  let failed = false
  let result: LearningRecommendationView | null = view
  await loader.load('kb', (value, error) => { result = value; failed = error })
  assert.equal(failed, true)
  assert.equal(result, null)
  assert.equal(JSON.stringify(graph), before)
})

test('older requests and invalidated KB requests cannot replace newer results', async () => {
  const resolves: ((v: LearningRecommendationView) => void)[] = []
  const loader = createRecommendationLoader(() => new Promise(resolve => resolves.push(resolve)))
  const commits: LearningRecommendationView[] = []
  const first = loader.load('kb', value => { if (value) commits.push(value) })
  const second = loader.load('kb', value => { if (value) commits.push(value) })
  resolves[1](view); await second
  resolves[0]({ ...view, recommendations: [] }); await first
  assert.deepEqual(commits, [view])
  const third = loader.load('kb', value => { if (value) commits.push(value) })
  loader.invalidate(); resolves[2](view); await third
  assert.equal(commits.length, 1)
})

// Compile and SSR-render the actual Vue components using dependencies already
// installed with Vue. This checks rendered debug/empty/error states, not a mock UI.
const require = createRequire(import.meta.url)
async function renderComponent(file: string, props: Record<string, unknown>) {
  const source = readFileSync(new URL(file, import.meta.url), 'utf8')
  const { descriptor } = parse(source)
  const compiled = compileScript(descriptor, { id: file, inlineTemplate: true })
  const js = ts.transpileModule(compiled.content, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  const module = { exports: {} as any }
  new Function('require', 'module', 'exports', js)(require, module, module.exports)
  const app = createSSRApp({ render: () => h(module.exports.default, props) })
  app.use(createI18n({ legacy: false, locale: 'en-US', messages: { 'en-US': en } }))
  app.component('t-link', defineComponent({ setup(_, { slots }) { return () => h('a', slots.default?.()) } }))
  return renderToString(app)
}

test('actual drawer renders supporting nodes/reasons and hides technical fields when debug is off', async () => {
  const html = await renderComponent('./LearningRecommendationDetails.vue', { item, view, debug: false })
  assert.match(html, /Why this is recommended/)
  assert.match(html, /Anchor/)
  assert.match(html, /familiar/i)
  assert.ok(!html.includes('score_components'))
  assert.ok(!html.includes('structural'))
  assert.ok(!html.includes('knowledge_base_id'))
  const debugHTML = await renderComponent('./LearningRecommendationDetails.vue', { item, view, debug: true })
  for (const field of ['structural', 'anchor_strength', 'multi_anchor', 'recency', 'long_term_memory', 'generated_at', 'traversed_paths']) assert.ok(debugHTML.includes(field))
})

test('actual cards render rank, normal empty state, no-Wiki state and non-blocking error', async () => {
  const props = { items: [item], loading: false, failed: false, wikiEnabled: true, truncated: false }
  const html = await renderComponent('./LearningRecommendationPanel.vue', props)
  assert.match(html, /#1 Candidate/)
  assert.match(html, /85%/)
  assert.match(await renderComponent('./LearningRecommendationPanel.vue', { ...props, items: [] }), /No related unknown candidates/)
  assert.match(await renderComponent('./LearningRecommendationPanel.vue', { ...props, wikiEnabled: false }), /Wiki is not enabled/)
  assert.match(await renderComponent('./LearningRecommendationPanel.vue', { ...props, failed: true }), /graph is still available/)
})
