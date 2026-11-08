import { ref } from 'vue'

export type Theme = 'dark' | 'light'

function read(): Theme {
  try {
    const t = localStorage.getItem('im-theme')
    if (t === 'light' || t === 'dark') return t
  } catch {
    /* storage unavailable */
  }
  return 'dark'
}

export const theme = ref<Theme>(read())

export function setTheme(t: Theme) {
  theme.value = t
  document.documentElement.setAttribute('data-theme', t)
  try {
    localStorage.setItem('im-theme', t)
  } catch {
    /* storage unavailable */
  }
}

export function toggleTheme() {
  setTheme(theme.value === 'dark' ? 'light' : 'dark')
}
