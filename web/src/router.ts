import { createRouter, createWebHistory } from 'vue-router'
import { ApiError } from './api/client'
import { qk } from './api/keys'
import { fetchMe } from './composables/useMe'
import { queryClient } from './queryClient'

declare module 'vue-router' {
  interface RouteMeta {
    public?: boolean
    title?: string
    role?: 'viewer' | 'operator' | 'admin'
  }
}

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/incidents' },
    { path: '/login', name: 'login', component: () => import('./views/LoginView.vue'), meta: { public: true, title: 'Sign in' } },
    { path: '/incidents', name: 'incidents', component: () => import('./views/IncidentsView.vue'), meta: { title: 'Incidents' } },
    {
      path: '/incidents/:id(\\d+)',
      name: 'incident',
      component: () => import('./views/IncidentPage.vue'),
      props: (r) => ({ id: Number(r.params.id) }),
      meta: { title: 'Incident' },
    },
    { path: '/mutes', name: 'mutes', component: () => import('./views/MutesView.vue'), meta: { title: 'Mutes' } },
    { path: '/status', name: 'status', component: () => import('./views/StatusView.vue'), meta: { title: 'Status' } },
    { path: '/profile', name: 'profile', component: () => import('./views/ProfileView.vue'), meta: { title: 'Profile' } },
    { path: '/admin', name: 'admin', component: () => import('./views/AdminView.vue'), meta: { title: 'Admin', role: 'admin' } },
    { path: '/:pathMatch(.*)*', name: 'notfound', component: () => import('./views/NotFound.vue'), meta: { title: 'Not found' } },
  ],
})

router.beforeEach(async (to) => {
  if (to.meta.public) return true
  try {
    const me = await queryClient.ensureQueryData({ queryKey: qk.me, queryFn: fetchMe, staleTime: 60_000 })
    if (to.meta.role === 'admin' && me.role !== 'admin') return { name: 'incidents' }
    return true
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) {
      return { name: 'login', query: { redirect: to.fullPath } }
    }
    // backend unreachable: still render, views show their own errors
    return true
  }
})

router.afterEach((to) => {
  document.title = to.meta.title ? `${to.meta.title} · infra-monitor` : 'infra-monitor'
})
