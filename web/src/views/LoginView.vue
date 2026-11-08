<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { api, errorMessage, setCsrfToken } from '@/api/client'
import { qk } from '@/api/keys'
import Btn from '@/components/ui/Btn.vue'
import Icon from '@/components/ui/Icon.vue'
import Skeleton from '@/components/ui/Skeleton.vue'

const route = useRoute()
const router = useRouter()
const qc = useQueryClient()
const redirect = computed(() => {
  const r = route.query.redirect
  const s = typeof r === 'string' ? r : '/incidents'
  return s.startsWith('/') && !s.startsWith('//') && !s.startsWith('/login') ? s : '/incidents'
})

const cfg = useQuery({ queryKey: qk.authConfig, queryFn: api.authConfig, retry: 1 })
const email = ref('')
const password = ref('')
const error = ref(route.query.error ? String(route.query.error) : '')
const busy = ref(false)

async function submit() {
  error.value = ''
  busy.value = true
  try {
    const me = await api.login(email.value.trim(), password.value)
    setCsrfToken(me.csrf_token)
    qc.setQueryData(qk.me, me)
    await router.replace(redirect.value)
  } catch (e) {
    error.value = errorMessage(e)
  } finally {
    busy.value = false
  }
}
const oidcHref = computed(() => api.oidcLoginURL(redirect.value))
</script>

<template>
  <div class="login">
    <div class="login__glow" aria-hidden="true" />
    <main class="login__card">
      <div class="login__brand">
        <svg viewBox="0 0 32 32" width="30" height="30" aria-hidden="true">
          <path d="M4 18h5l3-8 5 14 3-9 2 3h6" fill="none" stroke="var(--accent)" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
        <div>
          <h1>infra-monitor</h1>
          <p class="muted">Incidents, AI investigation and actions</p>
        </div>
      </div>

      <Skeleton v-if="cfg.isPending.value" :lines="4" :height="28" />
      <p v-else-if="cfg.isError.value" class="login__err">Cannot reach the server: {{ errorMessage(cfg.error.value) }}</p>
      <template v-else>
        <a v-if="cfg.data.value?.oidc_enabled" :href="oidcHref" class="login__sso">
          <Icon name="key" :size="16" /> Sign in with {{ cfg.data.value.oidc_name || 'SSO' }}
        </a>
        <div v-if="cfg.data.value?.oidc_enabled && cfg.data.value?.local_enabled" class="login__or"><span>or</span></div>
        <form v-if="cfg.data.value?.local_enabled" class="login__form" @submit.prevent="submit">
          <label class="label" for="email">Email</label>
          <input id="email" v-model="email" class="input" type="email" autocomplete="username" required />
          <label class="label" for="password">Password</label>
          <input id="password" v-model="password" class="input" type="password" autocomplete="current-password" required />
          <p v-if="error" class="login__err" role="alert">{{ error }}</p>
          <Btn variant="primary" type="submit" :loading="busy" class="login__submit">Sign in</Btn>
        </form>
        <p v-else-if="error" class="login__err" role="alert">{{ error }}</p>
      </template>
    </main>
  </div>
</template>

<style scoped>
.login {
  min-height: 100%;
  display: grid;
  place-items: center;
  padding: 24px 16px;
  position: relative;
  overflow: hidden;
}
.login__glow {
  position: absolute;
  inset: auto -20% -40% -20%;
  height: 70%;
  background: radial-gradient(ellipse at center, var(--accent-soft), transparent 65%);
  pointer-events: none;
}
.login__card {
  position: relative;
  width: min(380px, 100%);
  background: var(--bg-elev);
  border: 1px solid var(--border);
  border-radius: 14px;
  padding: 28px 26px;
  box-shadow: var(--shadow-pop);
}
.login__brand {
  display: flex;
  gap: 12px;
  align-items: center;
  margin-bottom: 24px;
}
.login__brand h1 {
  font-size: var(--fs-xl);
}
.login__brand p {
  margin: 2px 0 0;
  font-size: var(--fs-sm);
}
.login__sso {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  height: 38px;
  border-radius: var(--radius);
  border: 1px solid var(--border-strong);
  background: var(--bg-panel);
  color: var(--text-strong);
  font-weight: 500;
}
.login__sso:hover {
  text-decoration: none;
  border-color: var(--accent);
}
.login__or {
  display: flex;
  align-items: center;
  gap: 10px;
  margin: 18px 0;
  color: var(--text-faint);
  font-size: var(--fs-xs);
  text-transform: uppercase;
  letter-spacing: 0.08em;
}
.login__or::before,
.login__or::after {
  content: '';
  flex: 1;
  height: 1px;
  background: var(--border);
}
.login__form {
  display: flex;
  flex-direction: column;
}
.login__form .label:not(:first-child) {
  margin-top: 14px;
}
.login__submit {
  margin-top: 20px;
  height: 36px;
}
.login__err {
  color: var(--danger);
  font-size: var(--fs-sm);
  margin: 12px 0 0;
}
</style>
