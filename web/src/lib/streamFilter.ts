import { ref, watch } from 'vue'

export type StreamFilter = 'all' | 'conversation'
const KEY = 'im-stream-filter'

function read(): StreamFilter {
  try {
    return localStorage.getItem(KEY) === 'conversation' ? 'conversation' : 'all'
  } catch {
    return 'all'
  }
}

/** "Everything" or "Conversation only" in the incident stream, remembered per browser. */
export const streamFilter = ref<StreamFilter>(read())

watch(streamFilter, (v) => {
  try {
    localStorage.setItem(KEY, v)
  } catch {
    /* storage unavailable */
  }
})
