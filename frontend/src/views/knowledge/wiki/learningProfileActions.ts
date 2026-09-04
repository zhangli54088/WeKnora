import type { LearningProfileExport } from '@/api/memory'

export const learningProfileFilename = (date = new Date()) =>
  `weknora-learning-profile-${date.toISOString().slice(0, 10)}.json`

export interface ProfileDownloadEnvironment {
  createURL: (blob: Blob) => string
  revokeURL: (url: string) => void
  save: (url: string, filename: string) => void
}

export function downloadLearningProfile(
  profile: LearningProfileExport,
  date = new Date(),
  environment: ProfileDownloadEnvironment = {
    createURL: blob => URL.createObjectURL(blob),
    revokeURL: url => URL.revokeObjectURL(url),
    save: (url, filename) => {
      const link = document.createElement('a')
      link.href = url
      link.download = filename
      document.body.appendChild(link)
      link.click()
      link.remove()
    },
  },
) {
  const url = environment.createURL(new Blob([JSON.stringify(profile, null, 2)], { type: 'application/json' }))
  try {
    environment.save(url, learningProfileFilename(date))
  } finally {
    environment.revokeURL(url)
  }
}

export interface ProfileActionState {
  exporting: boolean
  deleting: boolean
  confirming: boolean
}

// UI-independent orchestration keeps cancellation, duplicate clicks and
// failures testable without a browser or a real profile API.
export function createLearningProfileActions(state: ProfileActionState, ports: {
  exportProfile: () => Promise<{ success: boolean; data: LearningProfileExport }>
  deleteProfile: () => Promise<{ success: boolean }>
  download: (profile: LearningProfileExport) => void
  confirm: (accept: () => Promise<boolean>, cancel: () => void) => void
  cleared: () => void
  success: (action: 'export' | 'delete') => void
  error: (action: 'export' | 'delete') => void
}) {
  return {
    async exportProfile() {
      if (state.exporting || state.deleting) return
      state.exporting = true
      try {
        const response = await ports.exportProfile()
        if (!response.success) throw new Error('export_failed')
        ports.download(response.data)
        ports.success('export')
      } catch {
        // No raw transport error: it can contain request headers or payloads.
        ports.error('export')
      } finally {
        state.exporting = false
      }
    },
    requestDelete() {
      if (state.deleting || state.confirming || state.exporting) return
      state.confirming = true
      ports.confirm(async () => {
        if (state.deleting || !state.confirming) return false
        state.deleting = true
        try {
          const response = await ports.deleteProfile()
          if (!response.success) throw new Error('delete_failed')
          ports.cleared()
          ports.success('delete')
          state.confirming = false
          return true
        } catch {
          ports.error('delete')
          return false
        } finally {
          state.deleting = false
        }
      }, () => { if (!state.deleting) state.confirming = false })
    },
  }
}

// Generations invalidate in-flight evidence requests after a delete, a new
// selection or a workspace/KB switch. They do not cancel normal Wiki reads.
export function createProfileRequestGuard() {
  let generation = 0
  return {
    start: () => ++generation,
    invalidate: () => { generation++ },
    current: (request: number) => generation === request,
  }
}
