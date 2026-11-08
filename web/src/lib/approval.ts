import { nextTick, ref, shallowRef } from 'vue'
import type { Action } from '@/api/types'

/** The prepared action waiting for the "Approve exactly this change?" confirmation. */
export const approvalTarget = shallowRef<Action | null>(null)

export function requestApproval(a: Action) {
  approvalTarget.value = a
}

/** Action card to highlight briefly (e.g. the proposal that superseded the one being approved). */
export const highlightedAction = ref<number | null>(null)

export function revealAction(id: number) {
  highlightedAction.value = id
  void nextTick(() => {
    document.querySelector(`[data-action-id="${id}"]`)?.scrollIntoView({ block: 'center', behavior: 'smooth' })
  })
  window.setTimeout(() => {
    if (highlightedAction.value === id) highlightedAction.value = null
  }, 2500)
}

/** First 8 characters of a payload hash. */
export const hash8 = (h?: string) => (h ?? '').slice(0, 8)

/** Heuristic: unified diff output (lines starting with +, -, @@). */
export function looksLikeDiff(text: string): boolean {
  const lines = text.split('\n').slice(0, 80)
  let marks = 0
  for (const l of lines) if (/^(@@|\+|-|diff |--- |\+\+\+ )/.test(l)) marks++
  return marks >= 2 && lines.some((l) => /^(@@|[+-][^+-])/.test(l))
}
