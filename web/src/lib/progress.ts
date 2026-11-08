import { reactive } from 'vue'
import type { ProgressNotice } from '@/api/types'

export interface ProgressItem {
  kind: ProgressNotice['kind']
  tool?: string
  text?: string
  at: number
}

interface State {
  active: boolean
  items: ProgressItem[]
  updatedAt: number
}

const state = reactive<Record<number, State>>({})

export function progressFor(id: number): State {
  if (!state[id]) state[id] = { active: false, items: [], updatedAt: 0 }
  return state[id]
}

export function pushProgress(n: ProgressNotice) {
  const s = progressFor(n.incident_id)
  if (n.kind === 'done') {
    s.active = false
    s.updatedAt = Date.now()
    return
  }
  if (!s.active) s.items = []
  s.active = true
  s.items.push({ kind: n.kind, tool: n.tool, text: n.text, at: Date.now() })
  if (s.items.length > 40) s.items.splice(0, s.items.length - 40)
  s.updatedAt = Date.now()
}

/** Mark the agent as working right away (before the first progress event arrives). */
export function markThinking(id: number) {
  const s = progressFor(id)
  s.active = true
  s.items = []
  s.updatedAt = Date.now()
}
