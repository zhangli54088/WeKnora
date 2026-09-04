<template>
  <div class="learning-profile-actions">
    <t-button size="small" variant="outline" :loading="state.exporting" :disabled="state.deleting" @click="actions.exportProfile">
      <template #icon><t-icon name="download" /></template>
      {{ t('learningProfileData.export') }}
    </t-button>
    <t-dropdown trigger="click" :options="[{ content: t('learningProfileData.delete'), value: 'delete', theme: 'error' }]" @click="actions.requestDelete">
      <t-button size="small" variant="text" :disabled="state.exporting || state.deleting || state.confirming" :aria-label="t('learningProfileData.more')">
        <template #icon><t-icon name="more" /></template>
        {{ t('learningProfileData.more') }}
      </t-button>
    </t-dropdown>
  </div>
</template>

<script setup lang="ts">
import { reactive } from 'vue'
import { useI18n } from 'vue-i18n'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'
import { exportLearningProfile, deleteLearningProfile } from '@/api/memory'
import { createLearningProfileActions, downloadLearningProfile } from './learningProfileActions'

const emit = defineEmits<{ cleared: [] }>()
const { t } = useI18n()
const state = reactive({ exporting: false, deleting: false, confirming: false })
const actions = createLearningProfileActions(state, {
  exportProfile: exportLearningProfile,
  deleteProfile: deleteLearningProfile,
  download: downloadLearningProfile,
  cleared: () => emit('cleared'),
  success: action => MessagePlugin.success(t(`learningProfileData.${action}Success`)),
  error: action => MessagePlugin.error(t(`learningProfileData.${action}Failed`)),
  confirm: (accept, cancel) => {
    const dialog = DialogPlugin.confirm({
      header: t('learningProfileData.delete'),
      body: t('learningProfileData.confirmBody'),
      theme: 'danger',
      confirmBtn: { content: t('learningProfileData.delete'), theme: 'danger' },
      cancelBtn: t('common.cancel'),
      closeOnOverlayClick: false,
      closeOnEscKeydown: false,
      closeBtn: false,
      onCancel: () => { if (!state.deleting) { cancel(); dialog.hide() } },
      onConfirm: async () => {
        if (state.deleting) return
        dialog.update({ confirmLoading: true })
        try { if (await accept()) dialog.hide() }
        finally { dialog.update({ confirmLoading: false }) }
      },
    })
  },
})
</script>

<style scoped>
.learning-profile-actions {
  display: flex;
  align-items: center;
  gap: var(--td-comp-margin-xs);
  color: var(--td-text-color-primary);
}
</style>
