import { onScopeDispose, ref } from 'vue'

/** A reactive clock shared by relative timestamps. */
const now = ref(Date.now())
let users = 0
let timer: number | undefined

export function useNow() {
  users++
  if (!timer) timer = window.setInterval(() => (now.value = Date.now()), 1000)
  onScopeDispose(() => {
    users--
    if (users <= 0 && timer) {
      window.clearInterval(timer)
      timer = undefined
    }
  })
  return now
}
