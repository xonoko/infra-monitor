import { reactive } from 'vue'

export interface Toast {
  id: number
  title: string
  description?: string
  kind: 'info' | 'success' | 'error'
}

let seq = 0
export const toasts = reactive<Toast[]>([])

export function toast(title: string, opts: { description?: string; kind?: Toast['kind'] } = {}) {
  toasts.push({ id: ++seq, title, description: opts.description, kind: opts.kind ?? 'info' })
  if (toasts.length > 5) toasts.splice(0, toasts.length - 5)
}

export const toastError = (title: string, e: unknown) =>
  toast(title, { kind: 'error', description: e instanceof Error ? e.message : String(e) })
export const toastOk = (title: string, description?: string) => toast(title, { kind: 'success', description })

export function dismiss(id: number) {
  const i = toasts.findIndex((t) => t.id === id)
  if (i >= 0) toasts.splice(i, 1)
}
