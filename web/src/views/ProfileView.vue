<script setup lang="ts">
import { ref } from 'vue'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import { api } from '@/api/client'
import { qk } from '@/api/keys'
import type { LinkCode } from '@/api/types'
import { useMe } from '@/composables/useMe'
import Btn from '@/components/ui/Btn.vue'
import Chip from '@/components/ui/Chip.vue'
import Icon from '@/components/ui/Icon.vue'
import Skeleton from '@/components/ui/Skeleton.vue'
import { countdown } from '@/lib/format'
import { useNow } from '@/lib/now'
import { toast, toastError, toastOk } from '@/lib/toast'
import { theme, setTheme } from '@/lib/theme'

const { me, isPending } = useMe()
const qc = useQueryClient()
const now = useNow()
const code = ref<LinkCode | null>(null)

const gen = useMutation({
  mutationFn: api.linkCode,
  onSuccess: (c) => (code.value = c),
  onError: (e) => toastError('Could not generate a code', e),
})
const unlink = useMutation({
  mutationFn: (t: string) => api.unlinkIdentity(t),
  onSuccess: () => {
    toastOk('Identity unlinked')
    qc.invalidateQueries({ queryKey: qk.me })
  },
  onError: (e) => toastError('Could not unlink', e),
})

async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    toast('Copied to clipboard')
  } catch {
    toastError('Copy failed', 'Select the text and copy it manually')
  }
}
const roleText: Record<string, string> = {
  viewer: 'Read incidents, timelines and investigations.',
  operator: 'Chat with the agent, acknowledge, resolve, mute, and approve or reject actions.',
  admin: 'Everything operators can do, plus users and API tokens.',
}
</script>

<template>
  <div class="page narrow">
    <header class="page-header">
      <div>
        <h1>Profile</h1>
        <p>Your account and chat identities.</p>
      </div>
    </header>
    <Skeleton v-if="isPending" :lines="6" />
    <template v-else-if="me">
      <section class="panel box">
        <div class="who">
          <div class="who__avatar">{{ (me.display_name || me.email).slice(0, 1).toUpperCase() }}</div>
          <div>
            <div class="who__name">{{ me.display_name || me.email }}</div>
            <div class="mono muted small">{{ me.email }} · signed in with {{ me.auth_method }}</div>
          </div>
          <Chip kind="plain" :value="me.role" class="who__role" />
        </div>
        <p class="muted small role-help">{{ roleText[me.role] }}</p>
      </section>

      <section class="panel box">
        <h2 class="box__title"><Icon name="telegram" :size="15" /> Chat identities</h2>
        <p class="muted small">Link your Telegram account so you can approve actions and chat with the agent from the infra group. Unlinked users can only read.</p>
        <ul v-if="me.identities?.length" class="ids">
          <li v-for="i in me.identities" :key="i.channel_type">
            <Icon name="link" :size="14" />
            <span class="mono">{{ i.channel_type }}</span>
            <span>{{ i.external_name }}</span>
            <span class="spacer" />
            <Btn size="sm" variant="ghost" :loading="unlink.isPending.value" @click="unlink.mutate(i.channel_type)">Unlink</Btn>
          </li>
        </ul>
        <div class="link">
          <Btn icon="key" :loading="gen.isPending.value" @click="gen.mutate()">{{ code ? 'Generate a new code' : 'Generate Telegram link code' }}</Btn>
          <div v-if="code" class="code">
            <div class="code__cmd mono">
              <span>/link {{ code.code }}</span>
              <Btn size="sm" variant="ghost" icon="copy" aria-label="Copy command" @click="copy(`/link ${code.code}`)" />
            </div>
            <div class="small muted">
              {{ code.instructions || 'Send this command to the bot (in the infra group or in a private chat).' }}
              <span class="mono" :class="{ exp: new Date(code.expires_at).getTime() - now < 60e3 }">{{ countdown(code.expires_at, now) }}</span>
            </div>
          </div>
        </div>
      </section>

      <section class="panel box">
        <h2 class="box__title"><Icon name="sun" :size="15" /> Appearance</h2>
        <div class="seg" role="radiogroup" aria-label="Theme">
          <button role="radio" :aria-checked="theme === 'dark'" :class="{ on: theme === 'dark' }" @click="setTheme('dark')"><Icon name="moon" :size="13" /> Dark</button>
          <button role="radio" :aria-checked="theme === 'light'" :class="{ on: theme === 'light' }" @click="setTheme('light')"><Icon name="sun" :size="13" /> Light</button>
        </div>
      </section>
    </template>
  </div>
</template>

<style scoped>
.narrow {
  max-width: 760px;
}
.box {
  padding: 16px;
  margin-bottom: 14px;
}
.box__title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: var(--fs-md);
  margin-bottom: 8px;
}
.small {
  font-size: var(--fs-sm);
}
.who {
  display: flex;
  align-items: center;
  gap: 12px;
}
.who__avatar {
  width: 40px;
  height: 40px;
  border-radius: 50%;
  display: grid;
  place-items: center;
  background: var(--accent-soft);
  color: var(--accent);
  font-weight: 600;
  font-size: var(--fs-lg);
}
.who__name {
  color: var(--text-strong);
  font-weight: 600;
  font-size: var(--fs-lg);
}
.who__role {
  margin-left: auto;
}
.role-help {
  margin: 10px 0 0;
}
.ids {
  list-style: none;
  margin: 10px 0;
  padding: 0;
}
.ids li {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 0;
  border-bottom: 1px solid var(--border);
}
.spacer {
  flex: 1;
}
.link {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 12px;
  margin-top: 12px;
}
.code {
  width: 100%;
}
.code__cmd {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 12px;
  border-radius: var(--radius);
  background: var(--code-bg);
  border: 1px solid var(--accent-ring);
  font-size: var(--fs-lg);
  color: var(--accent);
  margin-bottom: 6px;
}
.exp {
  color: var(--danger);
}
.seg {
  display: inline-flex;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius);
  overflow: hidden;
}
.seg button {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  border: 0;
  background: transparent;
  color: var(--text-dim);
  padding: 0 14px;
  height: 30px;
}
.seg button.on {
  background: var(--bg-active);
  color: var(--text-strong);
}
</style>
