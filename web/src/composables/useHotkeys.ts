import { onBeforeUnmount, onMounted } from 'vue'

type Map = Record<string, (e: KeyboardEvent) => void>

function typing(e: KeyboardEvent) {
  const el = e.target as HTMLElement | null
  if (!el) return false
  const tag = el.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || el.isContentEditable
}

/** Single-key shortcuts, ignored while typing (except Escape). */
export function useHotkeys(map: Map) {
  const handler = (e: KeyboardEvent) => {
    if (e.metaKey || e.ctrlKey || e.altKey) return
    if (e.key !== 'Escape' && typing(e)) return
    const fn = map[e.key]
    if (fn) fn(e)
  }
  onMounted(() => window.addEventListener('keydown', handler))
  onBeforeUnmount(() => window.removeEventListener('keydown', handler))
}
