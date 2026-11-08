<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { useQueryClient } from '@tanstack/vue-query'
import { DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuPortal, DropdownMenuRoot, DropdownMenuSeparator, DropdownMenuTrigger } from 'reka-ui'
import Icon from './ui/Icon.vue'
import Tip from './ui/Tip.vue'
import { useMe } from '@/composables/useMe'
import { api } from '@/api/client'
import { theme, toggleTheme } from '@/lib/theme'
import { sseConnected } from '@/api/sse'

const { me, isAdmin } = useMe()
const router = useRouter()
const qc = useQueryClient()

const items = computed(() => [
  { to: '/incidents', icon: 'incidents', label: 'Incidents' },
  { to: '/mutes', icon: 'mute', label: 'Mutes' },
  { to: '/status', icon: 'status', label: 'Status' },
  ...(isAdmin.value ? [{ to: '/admin', icon: 'admin', label: 'Admin' }] : []),
])

const initials = computed(() => {
  const n = me.value?.display_name || me.value?.email || '?'
  return n
    .split(/[\s@.]+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((s) => s[0]?.toUpperCase())
    .join('')
})

async function logout() {
  try {
    await api.logout()
  } finally {
    qc.clear()
    void router.push('/login')
  }
}
</script>

<template>
  <nav class="side" aria-label="Main">
    <RouterLink to="/incidents" class="side__logo" aria-label="infra-monitor home">
      <svg viewBox="0 0 32 32" width="26" height="26" aria-hidden="true">
        <path d="M4 18h5l3-8 5 14 3-9 2 3h6" fill="none" stroke="var(--accent)" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round" />
      </svg>
    </RouterLink>
    <div class="side__items">
      <Tip v-for="it in items" :key="it.to" :text="it.label" side="right">
        <RouterLink :to="it.to" class="side__item" active-class="side__item--active" :aria-label="it.label">
          <Icon :name="it.icon" :size="18" />
        </RouterLink>
      </Tip>
    </div>
    <div class="side__bottom">
      <Tip :text="sseConnected ? 'Live updates connected' : 'Live updates disconnected — retrying'" side="right">
        <span class="side__live" :class="{ 'side__live--on': sseConnected }" role="status" :aria-label="sseConnected ? 'Live' : 'Offline'" />
      </Tip>
      <Tip :text="theme === 'dark' ? 'Light theme' : 'Dark theme'" side="right">
        <button class="side__item" :aria-label="theme === 'dark' ? 'Switch to light theme' : 'Switch to dark theme'" @click="toggleTheme">
          <Icon :name="theme === 'dark' ? 'sun' : 'moon'" :size="17" />
        </button>
      </Tip>
      <DropdownMenuRoot>
        <DropdownMenuTrigger class="side__avatar" aria-label="Account menu">{{ initials }}</DropdownMenuTrigger>
        <DropdownMenuPortal>
          <DropdownMenuContent class="menu" side="right" align="end" :side-offset="8">
            <DropdownMenuLabel class="menu__label">
              <div class="menu__name">{{ me?.display_name || me?.email }}</div>
              <div class="menu__sub mono">{{ me?.email }} · {{ me?.role }}</div>
            </DropdownMenuLabel>
            <DropdownMenuSeparator class="menu__sep" />
            <DropdownMenuItem class="menu__item" @select="router.push('/profile')"><Icon name="user" />Profile & chat linking</DropdownMenuItem>
            <DropdownMenuItem class="menu__item" @select="toggleTheme"><Icon :name="theme === 'dark' ? 'sun' : 'moon'" />Toggle theme</DropdownMenuItem>
            <DropdownMenuSeparator class="menu__sep" />
            <DropdownMenuItem class="menu__item" @select="logout"><Icon name="logout" />Sign out</DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenuPortal>
      </DropdownMenuRoot>
    </div>
  </nav>
</template>

<style scoped>
.side {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
  padding: 10px 0;
  background: var(--bg-sunken);
  border-right: 1px solid var(--border);
  height: 100%;
}
.side__logo {
  display: grid;
  place-items: center;
  width: 36px;
  height: 36px;
  border-radius: 8px;
  margin-bottom: 6px;
}
.side__items,
.side__bottom {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
}
.side__bottom {
  margin-top: auto;
  gap: 8px;
}
.side__item {
  display: grid;
  place-items: center;
  width: 36px;
  height: 36px;
  border-radius: 8px;
  color: var(--text-dim);
  border: 0;
  background: transparent;
  position: relative;
  transition: background var(--dur) var(--ease), color var(--dur) var(--ease);
}
.side__item:hover {
  background: var(--bg-hover);
  color: var(--text-strong);
  text-decoration: none;
}
.side__item--active {
  background: var(--accent-soft);
  color: var(--accent);
}
.side__item--active::before {
  content: '';
  position: absolute;
  left: -8px;
  top: 9px;
  bottom: 9px;
  width: 2px;
  border-radius: 2px;
  background: var(--accent);
}
.side__avatar {
  width: 30px;
  height: 30px;
  border-radius: 50%;
  border: 1px solid var(--border-strong);
  background: var(--bg-active);
  color: var(--text-strong);
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.02em;
}
.side__live {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--text-faint);
  margin-bottom: 4px;
}
.side__live--on {
  background: var(--accent);
  box-shadow: 0 0 0 3px var(--accent-soft);
}
@media (max-width: 640px) {
  .side {
    flex-direction: row;
    height: auto;
    padding: 6px 12px;
    border-right: 0;
    border-top: 1px solid var(--border);
    justify-content: space-between;
  }
  .side__items,
  .side__bottom {
    flex-direction: row;
    margin-top: 0;
  }
  .side__logo {
    display: none;
  }
  .side__item--active::before {
    display: none;
  }
  .side__live {
    margin: 0;
  }
}
</style>

<style>
.menu {
  min-width: 220px;
  background: var(--bg-elev);
  border-radius: var(--radius);
  box-shadow: var(--shadow-pop);
  padding: 4px;
  z-index: 80;
}
.menu__label {
  padding: 8px 10px;
}
.menu__name {
  color: var(--text-strong);
  font-weight: 600;
}
.menu__sub {
  color: var(--text-dim);
  font-size: var(--fs-xs);
  margin-top: 2px;
}
.menu__sep {
  height: 1px;
  background: var(--border);
  margin: 4px 0;
}
.menu__item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 10px;
  border-radius: 4px;
  color: var(--text);
  cursor: pointer;
  outline: none;
  font-size: var(--fs-md);
}
.menu__item[data-highlighted] {
  background: var(--bg-hover);
  color: var(--text-strong);
}
.menu__item[data-disabled] {
  opacity: 0.4;
  cursor: default;
}
</style>
