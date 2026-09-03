<template>
  <section class="recommendation-details">
    <h3>{{ $t('learningRecommendation.why') }}</h3>
    <p>{{ $t('learningRecommendation.rank', { rank: item.rank }) }} · {{ $t('learningRecommendation.score', { score: Math.round(item.score * 100) }) }}</p>
    <ul><li v-for="reason in recommendationReasonLabels(item, t)" :key="reason">{{ reason }}</li></ul>
    <h4>{{ $t('learningRecommendation.anchors') }}</h4>
    <ul>
      <li v-for="node in item.supporting_nodes" :key="node.wiki_page_id">
        {{ node.title }} · {{ knowledgeStatusLabel(node.status) }}
      </li>
    </ul>
    <dl v-if="debug" class="recommendation-debug">
      <template v-for="[key, value] in recommendationDebugEntries(item, view)" :key="key">
        <dt>{{ key }}</dt><dd><code>{{ value }}</code></dd>
      </template>
    </dl>
  </section>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { LearningRecommendation, LearningRecommendationView, KnowledgeStatus } from '@/api/memory'
import { recommendationReasonLabels, recommendationDebugEntries } from './learningRecommendations'

defineProps<{ item: LearningRecommendation; view: LearningRecommendationView | null; debug: boolean }>()
const { t } = useI18n()
function knowledgeStatusLabel(status: KnowledgeStatus) {
  return t(`knowledgeEditor.wikiBrowser.status${status.charAt(0).toUpperCase()}${status.slice(1)}`)
}
</script>

<style scoped lang="less">
.recommendation-details { padding: 10px; margin: 12px 0; border: 1px dashed var(--td-warning-color); border-radius: 6px; }
h3, h4 { margin: 6px 0; font-size: 14px; }
p, li { font-size: 12px; line-height: 1.7; }
.recommendation-debug { display: grid; grid-template-columns: minmax(100px, auto) 1fr; gap: 6px; font-size: 11px; }
dd { margin: 0; overflow-wrap: anywhere; }
</style>
