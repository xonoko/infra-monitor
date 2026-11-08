import type { QueryClient } from '@tanstack/vue-query'
import { API_BASE } from './client'
import { qk } from './keys'
import type { ProgressNotice, TimelineNotice } from './types'
import { pushProgress } from '@/lib/progress'
import { ref } from 'vue'

export const sseConnected = ref(false)

type Handlers = {
  onTimeline?: (n: TimelineNotice) => void
  onProgress?: (n: ProgressNotice) => void
  onResync?: () => void
  onOpen?: () => void
  onError?: () => void
}

/** EventSource with exponential reconnect. Returns a close function. */
export function openStream(url: string, h: Handlers): () => void {
  let es: EventSource | null = null
  let closed = false
  let delay = 1000
  let timer: number | undefined

  const connect = () => {
    if (closed) return
    es = new EventSource(url, { withCredentials: true })
    es.addEventListener('open', () => {
      delay = 1000
      h.onOpen?.()
    })
    es.addEventListener('timeline', (e) => {
      try {
        h.onTimeline?.(JSON.parse((e as MessageEvent).data))
      } catch {
        /* ignore malformed */
      }
    })
    es.addEventListener('progress', (e) => {
      try {
        h.onProgress?.(JSON.parse((e as MessageEvent).data))
      } catch {
        /* ignore malformed */
      }
    })
    es.addEventListener('resync', () => h.onResync?.())
    es.addEventListener('error', () => {
      h.onError?.()
      es?.close()
      es = null
      if (closed) return
      timer = window.setTimeout(connect, delay)
      delay = Math.min(delay * 2, 30000)
    })
  }
  connect()
  return () => {
    closed = true
    if (timer) window.clearTimeout(timer)
    es?.close()
  }
}

function throttle(fn: () => void, ms: number) {
  let last = 0
  let pending: number | undefined
  return () => {
    const now = Date.now()
    const run = () => {
      last = Date.now()
      pending = undefined
      fn()
    }
    if (now - last >= ms) run()
    else if (!pending) pending = window.setTimeout(run, ms - (now - last))
  }
}

export function invalidateIncident(qc: QueryClient, id: number) {
  for (const key of [qk.incident(id), qk.timeline(id), qk.messages(id), qk.actions(id), qk.investigations(id), qk.signals(id)]) {
    qc.invalidateQueries({ queryKey: key })
  }
}

/** Global stream: keeps every list and open incident fresh. */
export function startGlobalStream(qc: QueryClient): () => void {
  const refreshLists = throttle(() => {
    qc.invalidateQueries({ queryKey: qk.incidents() })
    qc.invalidateQueries({ queryKey: qk.histogram() })
  }, 1500)
  return openStream(`${API_BASE}/events`, {
    onOpen: () => (sseConnected.value = true),
    onError: () => (sseConnected.value = false),
    onTimeline: (n) => {
      invalidateIncident(qc, n.incident_id)
      if (n.type.startsWith('mute_')) qc.invalidateQueries({ queryKey: qk.mutes })
      refreshLists()
      if (n.type === 'message' || n.type === 'investigation_completed' || n.type === 'investigation_failed') {
        pushProgress({ incident_id: n.incident_id, kind: 'done' })
      }
    },
    onResync: () => qc.invalidateQueries(),
  })
}

/** Per-incident stream (open while the drawer shows an incident): agent progress. */
export function startIncidentStream(id: number): () => void {
  return openStream(`${API_BASE}/events?incident_id=${id}`, {
    onProgress: (n) => {
      if (n.incident_id === id) pushProgress(n)
    },
  })
}
