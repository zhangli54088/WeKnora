<template>
  <section class="learning-recommendations" aria-live="polite">
    <h3>{{ $t('learningRecommendation.title') }}</h3>
    <p class="recommendation-note">{{ $t('learningRecommendation.uncertain') }}</p>
    <p v-if="loading">{{ $t('learningRecommendation.loading') }}</p>
    <p v-else-if="failed" class="recommendation-error">
      {{ $t('learningRecommendation.failed') }}
      <t-link @click="$emit('refresh')">{{ $t('common.retry') }}</t-link>
    </p>
    <p v-else-if="!wikiEnabled">{{ $t('learningRecommendation.noWiki') }}</p>
    <p v-else-if="items.length === 0">{{ $t('learningRecommendation.empty') }}</p>
    <div v-else class="recommendation-cards">
      <button v-for="item in items" :key="item.wiki_page_id" type="button" class="recommendation-card" @click="$emit('select', item)">
        <strong>#{{ item.rank }} {{ item.title }}</strong>
        <span>{{ $t('learningRecommendation.score', { score: Math.round(item.score * 100) }) }}</span>
        <span>{{ recommendationReasonLabels(item, t)[0] }}</span>
      </button>
    </div>
    <p v-if="truncated" class="recommendation-note">{{ $t('learningRecommendation.truncated') }}</p>
  </section>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { LearningRecommendation } from '@/api/memory'
import { recommendationReasonLabels } from './learningRecommendations'

defineProps<{ items: LearningRecommendation[]; loading: boolean; failed: boolean; wikiEnabled: boolean; truncated: boolean }>()
defineEmits<{ select: [item: LearningRecommendation]; refresh: [] }>()
const { t } = useI18n()
</script>

<style scoped lang="less">
.learning-recommendations { border-top: 1px solid var(--td-component-stroke); margin-top: 10px; padding-top: 8px; }
h3 { margin: 0 0 6px; font-size: 14px; }
p { font-size: 12px; margin: 6px 0; }
.recommendation-note { color: var(--td-text-color-secondary); }
.recommendation-error { color: var(--td-error-color); }
.recommendation-cards { display: grid; grid-template-columns: repeat(auto-fit, minmax(170px, 1fr)); gap: 8px; }
.recommendation-card {
  display: flex; flex-direction: column; gap: 4px; padding: 8px; text-align: left; cursor: pointer;
  border: 1px solid var(--td-warning-color); border-radius: 6px; background: var(--td-bg-color-container);
  color: var(--td-text-color-primary); font: inherit; font-size: 12px;
  &:hover, &:focus-visible { background: var(--td-warning-color-light); outline: 2px solid var(--td-warning-color); }
}
</style>
