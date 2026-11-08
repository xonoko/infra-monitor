import { createApp } from 'vue'
import { VueQueryPlugin } from '@tanstack/vue-query'
import '@fontsource/inter/400.css'
import '@fontsource/inter/500.css'
import '@fontsource/inter/600.css'
import '@fontsource/jetbrains-mono/400.css'
import '@fontsource/jetbrains-mono/500.css'
import './styles/tokens.css'
import './styles/base.css'
import App from './App.vue'
import { router } from './router'
import { queryClient } from './queryClient'

async function boot() {
  // Dev-only fixtures: `npm run dev:mock`. import.meta.env.DEV is false in production builds,
  // so this branch and the mock module are removed from the bundle.
  if (import.meta.env.DEV && import.meta.env.VITE_MOCK) {
    const { installMock } = await import('./mock/install')
    installMock()
  }
  createApp(App).use(VueQueryPlugin, { queryClient }).use(router).mount('#app')
}

void boot()
