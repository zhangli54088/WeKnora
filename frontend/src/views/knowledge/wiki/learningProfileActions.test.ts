import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { parse, compileScript } from '@vue/compiler-sfc'
import { computed, createRenderer, defineComponent, h, nextTick, ref } from 'vue'
import { createI18n } from 'vue-i18n'
import ts from 'typescript'
import en from '../../../i18n/locales/en-US'
import zh from '../../../i18n/locales/zh-CN'
import type { LearningProfileExport } from '@/api/memory'
import * as helpers from './learningProfileActions'
import { mergePersonalKnowledgeGraph, summarizeKnowledgeStates } from './personalLearningGraph'

const emptyProfile: LearningProfileExport = {
  version: 1, exported_at: '2026-09-03T00:00:00Z', scope: { tenant_id: 7, subject_id: 'web_user:alice' },
  memory: { items: [], topics: [], documents: [] },
  learning_profile: { memory_wiki_links: [], learning_evidences: [], knowledge_states: [] },
}
const require = createRequire(import.meta.url)
function evaluate(source: string, overrides: Record<string, unknown>) {
  const js = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  const module = { exports: {} as any }
  new Function('require', 'module', 'exports', js)((name: string) => overrides[name] ?? require(name), module, module.exports)
  return module.exports
}

test('learning profile APIs reuse the opt-in export and scoped DELETE without client scope', async () => {
  const calls: unknown[][] = []
  const api = evaluate(readFileSync(new URL('../../../api/memory.ts', import.meta.url), 'utf8'), {
    '@/utils/request': { get: (...args: unknown[]) => calls.push(['GET', ...args]), del: (...args: unknown[]) => calls.push(['DELETE', ...args]) },
  })
  await api.exportLearningProfile()
  await api.deleteLearningProfile()
  await api.exportMemoryItems()
  assert.deepEqual(calls, [
    ['GET', '/api/v1/memory/export?include_learning_profile=true'],
    ['DELETE', '/api/v1/memory/learning-profile'],
    ['GET', '/api/v1/memory/export'],
  ])
})

test('JSON download includes the full document, date filename, and revokes the object URL', async () => {
  let blob: Blob | undefined
  const saved: string[][] = []
  const revoked: string[] = []
  helpers.downloadLearningProfile(emptyProfile, new Date('2026-09-03T12:00:00Z'), {
    createURL: value => { blob = value; return 'blob:profile' },
    save: (url, filename) => saved.push([url, filename]),
    revokeURL: url => revoked.push(url),
  })
  assert.equal(blob?.type, 'application/json')
  assert.deepEqual(JSON.parse(await blob!.text()), emptyProfile)
  assert.deepEqual(saved, [['blob:profile', 'weknora-learning-profile-2026-09-03.json']])
  assert.deepEqual(revoked, ['blob:profile'])
})

// Mount the actual Vue action component with a tiny Vue host renderer. Only
// transport and TDesign primitives are replaced; real button/dialog callbacks
// run unchanged. No DOM library or new UI/test dependency is needed.
type HostNode = { type: string; props: Record<string, any>; children: HostNode[]; parent?: HostNode; text?: string }
function node(type: string): HostNode { return { type, props: {}, children: [] } }
async function mountActions(failExport = false, failDelete = false) {
  const dialogs: any[] = []
  const messages: string[] = []
  let exports = 0, deletes = 0, cleared = 0, hidden = 0
  const source = readFileSync(new URL('./LearningProfileActions.vue', import.meta.url), 'utf8')
  const compiled = compileScript(parse(source).descriptor, { id: 'profile-actions', inlineTemplate: true })
  const component = evaluate(compiled.content, {
    './learningProfileActions': { ...helpers, downloadLearningProfile: () => {} },
    '@/api/memory': {
      exportLearningProfile: async () => { exports++; if (failExport) throw new Error('Authorization: SECRET'); return { success: true, data: emptyProfile } },
      deleteLearningProfile: async () => { deletes++; if (failDelete) throw new Error('Cookie: SECRET'); return { success: true } },
    },
    'tdesign-vue-next': {
      MessagePlugin: { success: (message: string) => messages.push(message), error: (message: string) => messages.push(message) },
      DialogPlugin: { confirm: (options: unknown) => { dialogs.push(options); return { hide: () => hidden++, update: () => {} } } },
    },
  }).default
  const renderer = createRenderer<HostNode, HostNode>({
    createElement: node, createText: text => ({ ...node('text'), text }), createComment: text => ({ ...node('comment'), text }),
    setText: (el, text) => { el.text = text }, setElementText: (el, text) => { el.text = text },
    parentNode: el => el.parent ?? null, nextSibling: () => null,
    patchProp: (el, key, _old, value) => { el.props[key] = value },
    insert: (el, parent) => { el.parent = parent; parent.children.push(el) }, remove: () => {},
  })
  const app = renderer.createApp({ render: () => h(component, { onCleared: () => cleared++ }) })
  app.use(createI18n({ legacy: false, locale: 'en-US', messages: { 'en-US': en } }))
  for (const name of ['t-button', 't-dropdown', 't-icon']) {
    app.component(name, defineComponent({ inheritAttrs: false, setup(_, { attrs, slots }) { return () => h(name, attrs, slots.default?.()) } }))
  }
  const root = node('root')
  app.mount(root)
  const find = (type: string, current = root): HostNode[] => [ ...(current.type === type ? [current] : []), ...current.children.flatMap(child => find(type, child)) ]
  return { find, dialogs, messages, counts: () => ({ exports, deletes, cleared, hidden }), unmount: () => app.unmount() }
}

test('actual export button calls API; errors show a safe toast without clearing graph/profile', async () => {
  for (const fail of [false, true]) {
    const ui = await mountActions(fail)
    await ui.find('t-button')[0].props.onClick()
    assert.deepEqual(ui.counts(), { exports: 1, deletes: 0, cleared: 0, hidden: 0 })
    assert.match(ui.messages[0], fail ? /Export failed/ : /exported/)
    assert.ok(!ui.messages.join('').includes('SECRET'))
    ui.unmount()
  }
})

test('actual More action opens danger dialog; cancellation sends no DELETE; confirmation sends one', async () => {
  const ui = await mountActions()
  ui.find('t-dropdown')[0].props.onClick()
  assert.equal(ui.dialogs.length, 1)
  assert.equal(ui.dialogs[0].theme, 'danger')
  assert.equal(ui.dialogs[0].confirmBtn.theme, 'danger')
  assert.match(ui.dialogs[0].body, /NOT delete long-term memories, knowledge base \(KB\) documents, Wiki pages/)
  ui.dialogs[0].onCancel()
  assert.equal(ui.counts().deletes, 0)
  ui.find('t-dropdown')[0].props.onClick()
  await Promise.all([ui.dialogs[1].onConfirm(), ui.dialogs[1].onConfirm()])
  assert.deepEqual(ui.counts(), { exports: 0, deletes: 1, cleared: 1, hidden: 2 })
  ui.unmount()
})

test('failed deletion keeps profile and graph, dialog stays available for retry', async () => {
  const ui = await mountActions(false, true)
  ui.find('t-dropdown')[0].props.onClick()
  await ui.dialogs[0].onConfirm()
  assert.deepEqual(ui.counts(), { exports: 0, deletes: 1, cleared: 0, hidden: 0 })
  assert.match(ui.messages[0], /Deletion failed/)
  assert.ok(!ui.messages[0].includes('SECRET'))
  ui.unmount()
})

test('actual WikiBrowser clear callback keeps graph, resets unknown/stats/overlay, invalidates requests and refreshes', async () => {
  const source = readFileSync(new URL('./WikiBrowser.vue', import.meta.url), 'utf8')
  const script = parse(source).descriptor.scriptSetup!.content
  const ast = ts.createSourceFile('wiki.ts', script, ts.ScriptTarget.Latest, true)
  const fn = ast.statements.find(statement => ts.isFunctionDeclaration(statement) && statement.name?.text === 'onLearningProfileCleared')!
  const graph = { nodes: [{ id: 'a', slug: 'a', title: 'A', knowledge_base_id: 'kb', page_type: 'concept', link_count: 0 }], edges: [], meta: { mode: 'overview' as const, total: 1, returned: 1, truncated: false } }
  const original = JSON.stringify(graph)
  const guard = helpers.createProfileRequestGuard()
  const oldRequest = guard.start()
  let recommendationInvalidations = 0, refreshes = 0, renders = 0
  const scope: Record<string, any> = {
    graphDrawerPage: ref({ id: 'a', title: 'A' }),
    personalStatesRequest: 1, personalEvidenceRequests: guard,
    personalDebug: ref(true),
    recommendationLoader: { invalidate: () => recommendationInvalidations++ },
    personalStates: ref([{ wiki_page_id: 'a', status: 'familiar', evidence_count: 1 }]),
    personalEvidence: ref([{ id: 'evidence' }]), recommendationView: ref({ recommendations: [{ wiki_page_id: 'a' }] }),
    personalFilters: { query: 'a', status: 'familiar', litOnly: true, recommendedOnly: true },
    personalLastUpdated: ref('old'),
    renderGraph: () => renders++, refreshPersonalProfile: async () => { refreshes++ },
  }
  for (const key of ['personalStatesLoading', 'personalStatesError', 'personalEvidenceLoading', 'personalEvidenceError', 'recommendationsLoading', 'recommendationsFailed']) scope[key] = ref(true)
  scope.activeRecommendations = computed(() => scope.recommendationView.value?.recommendations ?? [])
  const selectedNames = ['selectedKnowledgeState', 'selectedRecommendation']
  const selectedCode = ast.statements.filter(statement => ts.isVariableStatement(statement) && statement.declarationList.declarations.some(declaration => selectedNames.includes(declaration.name.getText(ast)))).map(statement => statement.getText(ast)).join('\n')
  const selectedJS = ts.transpileModule(selectedCode, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText
  const selected = new Function('scope', 'computed', `with (scope) { ${selectedJS}; return { selectedKnowledgeState, selectedRecommendation } }`)(scope, computed)
  assert.equal(selected.selectedKnowledgeState.value.status, 'familiar')
  assert.equal(selected.selectedRecommendation.value.wiki_page_id, 'a')
  const run = new Function('scope', `with (scope) { ${fn.getText(ast)}; return onLearningProfileCleared() }`)
  await run(scope)
  await nextTick()
  assert.equal(JSON.stringify(graph), original)
  assert.equal(guard.current(oldRequest), false)
  assert.equal(scope.personalStatesRequest, 2)
  assert.equal(scope.personalDebug.value, false)
  assert.equal(recommendationInvalidations, 1)
  assert.equal(refreshes, 1); assert.equal(renders, 1)
  assert.deepEqual(scope.personalStates.value, [])
  assert.deepEqual(scope.personalEvidence.value, [])
  assert.equal(scope.recommendationView.value, null)
  assert.equal(scope.graphDrawerPage.value.id, 'a')
  assert.equal(selected.selectedKnowledgeState.value?.status ?? 'unknown', 'unknown')
  assert.equal(selected.selectedRecommendation.value, undefined)
  for (const key of ['personalStatesLoading', 'personalStatesError', 'personalEvidenceLoading', 'personalEvidenceError', 'recommendationsLoading', 'recommendationsFailed']) assert.equal(scope[key].value, false)
  const nodes = mergePersonalKnowledgeGraph(graph, scope.personalStates.value, [])
  assert.equal(nodes[0].learning_status, 'unknown')
  assert.equal(nodes[0].recommendation, undefined)
  const stats = summarizeKnowledgeStates(nodes)
  for (const key of ['lit', 'exposed', 'familiar', 'mastered', 'evidence'] as const) assert.equal(stats[key], 0)
  assert.match(zh.learningProfileData.empty, /当前还没有学习画像。继续与该知识库交流后/)
  assert.ok(source.indexOf('<LearningProfileActions') > source.indexOf('v-if="isPersonalProfile"'))
  assert.ok(!fn.getText(ast).includes('graphData.value ='))
})

test('actions use theme tokens and existing TDesign primitives, no custom destructive modal', () => {
  const source = readFileSync(new URL('./LearningProfileActions.vue', import.meta.url), 'utf8')
  assert.match(source, /var\(--td-text-color-primary\)/)
  assert.match(source, /DialogPlugin.confirm/)
  assert.match(source, /<t-dropdown/)
  assert.doesNotMatch(source, /window.confirm|#[0-9a-fA-F]{3,8}/)
})
