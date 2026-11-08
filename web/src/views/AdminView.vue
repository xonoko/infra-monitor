<script setup lang="ts">
import { ref } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { SwitchRoot, SwitchThumb, TabsContent, TabsList, TabsRoot, TabsTrigger } from 'reka-ui'
import { api, errorMessage } from '@/api/client'
import { qk } from '@/api/keys'
import type { CreatedAPIToken, Role, User } from '@/api/types'
import Btn from '@/components/ui/Btn.vue'
import Modal from '@/components/ui/Modal.vue'
import Skeleton from '@/components/ui/Skeleton.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Icon from '@/components/ui/Icon.vue'
import { absTime, relTime } from '@/lib/format'
import { toast, toastError, toastOk } from '@/lib/toast'
import { useMe } from '@/composables/useMe'

const qc = useQueryClient()
const { me } = useMe()
const tab = ref('users')
const roles: Role[] = ['viewer', 'operator', 'admin']

// ---- users ----
const users = useQuery({ queryKey: qk.users, queryFn: () => api.users().then((r) => r.items) })
const updateUser = useMutation({
  mutationFn: (a: { id: number; body: { role?: Role; disabled?: boolean } }) => api.updateUser(a.id, a.body),
  onSuccess: () => {
    toastOk('User updated')
    qc.invalidateQueries({ queryKey: qk.users })
  },
  onError: (e) => toastError('Could not update user', e),
})
const creatingUser = ref(false)
const nu = ref({ email: '', display_name: '', role: 'viewer' as Role, password: '' })
const nuError = ref('')
const createUser = useMutation({
  mutationFn: () =>
    api.createUser({
      email: nu.value.email.trim(),
      display_name: nu.value.display_name || undefined,
      role: nu.value.role,
      password: nu.value.password || undefined,
    }),
  onSuccess: () => {
    toastOk('User created')
    creatingUser.value = false
    nu.value = { email: '', display_name: '', role: 'viewer', password: '' }
    qc.invalidateQueries({ queryKey: qk.users })
  },
  onError: (e) => (nuError.value = errorMessage(e)),
})
const isSelf = (u: User) => u.id === me.value?.id

// ---- tokens ----
const tokens = useQuery({ queryKey: qk.tokens, queryFn: () => api.tokens().then((r) => r.items), enabled: () => tab.value === 'tokens' })
const creatingToken = ref(false)
const nt = ref({ name: '', role: 'operator' as Role, expires_in: '' })
const created = ref<CreatedAPIToken | null>(null)
const ntError = ref('')
const createToken = useMutation({
  mutationFn: () => api.createToken({ name: nt.value.name.trim(), role: nt.value.role, expires_in: nt.value.expires_in || undefined }),
  onSuccess: (t) => {
    created.value = t
    qc.invalidateQueries({ queryKey: qk.tokens })
  },
  onError: (e) => (ntError.value = errorMessage(e)),
})
const revoke = useMutation({
  mutationFn: (id: number) => api.revokeToken(id),
  onSuccess: () => {
    toastOk('Token revoked')
    qc.invalidateQueries({ queryKey: qk.tokens })
  },
  onError: (e) => toastError('Could not revoke token', e),
})
function closeToken() {
  creatingToken.value = false
  created.value = null
  nt.value = { name: '', role: 'operator', expires_in: '' }
  ntError.value = ''
}
async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    toast('Copied to clipboard')
  } catch {
    toastError('Copy failed', 'Select the token and copy it manually')
  }
}
</script>

<template>
  <div class="page">
    <header class="page-header">
      <div>
        <h1>Admin</h1>
        <p>Users and API tokens. Configuration itself lives in the Helm values (GitOps).</p>
      </div>
    </header>

    <TabsRoot v-model="tab">
      <TabsList class="atabs" aria-label="Admin sections">
        <TabsTrigger value="users" class="atab"><Icon name="user" :size="14" /> Users</TabsTrigger>
        <TabsTrigger value="tokens" class="atab"><Icon name="key" :size="14" /> API tokens</TabsTrigger>
      </TabsList>

      <TabsContent value="users">
        <div class="bar"><Btn variant="primary" icon="plus" @click="creatingUser = true">New user</Btn></div>
        <div class="panel">
          <div v-if="users.isPending.value" class="pad"><Skeleton :lines="5" /></div>
          <EmptyState v-else-if="users.isError.value" icon="info" title="Could not load users" :text="errorMessage(users.error.value)" />
          <div v-else class="tbl-wrap">
            <table class="table">
              <thead>
                <tr>
                  <th>User</th>
                  <th>Role</th>
                  <th>Auth</th>
                  <th>Last login</th>
                  <th>Enabled</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="u in users.data.value" :key="u.id" :class="{ off: u.disabled }">
                  <td>
                    <div class="u__name">{{ u.display_name || u.email }} <span v-if="isSelf(u)" class="you mono">you</span></div>
                    <div class="mono faint small">{{ u.email }}</div>
                  </td>
                  <td>
                    <select
                      class="select mono role-sel"
                      :value="u.role"
                      :disabled="isSelf(u) || updateUser.isPending.value"
                      :aria-label="`Role of ${u.email}`"
                      @change="updateUser.mutate({ id: u.id, body: { role: ($event.target as HTMLSelectElement).value as Role } })"
                    >
                      <option v-for="r in roles" :key="r" :value="r">{{ r }}</option>
                    </select>
                  </td>
                  <td class="mono small">{{ u.auth?.join(', ') || '—' }}</td>
                  <td class="mono small" :title="absTime(u.last_login_at)">{{ relTime(u.last_login_at) }}</td>
                  <td>
                    <SwitchRoot
                      class="switch"
                      :model-value="!u.disabled"
                      :disabled="isSelf(u)"
                      :aria-label="`Enable ${u.email}`"
                      @update:model-value="(v: boolean) => updateUser.mutate({ id: u.id, body: { disabled: !v } })"
                    >
                      <SwitchThumb class="switch__thumb" />
                    </SwitchRoot>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </TabsContent>

      <TabsContent value="tokens">
        <div class="bar"><Btn variant="primary" icon="plus" @click="creatingToken = true">New token</Btn></div>
        <div class="panel">
          <div v-if="tokens.isPending.value" class="pad"><Skeleton :lines="4" /></div>
          <EmptyState v-else-if="tokens.isError.value" icon="info" title="Could not load tokens" :text="errorMessage(tokens.error.value)" />
          <EmptyState v-else-if="!tokens.data.value?.length" icon="key" title="No API tokens" text="Tokens authenticate automation with Authorization: Bearer." />
          <div v-else class="tbl-wrap">
            <table class="table">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Prefix</th>
                  <th>Role</th>
                  <th>Last used</th>
                  <th>Expires</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                <tr v-for="t in tokens.data.value" :key="t.id" :class="{ off: !!t.revoked_at }">
                  <td>{{ t.name }}</td>
                  <td class="mono small">{{ t.prefix }}…</td>
                  <td class="mono small">{{ t.role }}</td>
                  <td class="mono small">{{ relTime(t.last_used_at) }}</td>
                  <td class="mono small">{{ t.revoked_at ? 'revoked' : t.expires_at ? absTime(t.expires_at, true) : 'never' }}</td>
                  <td class="right">
                    <Btn v-if="!t.revoked_at" size="sm" variant="danger" :loading="revoke.isPending.value && revoke.variables.value === t.id" @click="revoke.mutate(t.id)">Revoke</Btn>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </TabsContent>
    </TabsRoot>

    <Modal v-model:open="creatingUser" title="New user" description="Local user. Leave the password empty for SSO-only users (matched by email).">
      <form id="nu-form" class="form" @submit.prevent="nuError = ''; createUser.mutate()">
        <label class="label" for="nu-email">Email</label>
        <input id="nu-email" v-model="nu.email" class="input" type="email" required />
        <label class="label" for="nu-name">Display name</label>
        <input id="nu-name" v-model="nu.display_name" class="input" />
        <label class="label" for="nu-role">Role</label>
        <select id="nu-role" v-model="nu.role" class="select mono">
          <option v-for="r in roles" :key="r" :value="r">{{ r }}</option>
        </select>
        <label class="label" for="nu-pw">Password</label>
        <input id="nu-pw" v-model="nu.password" class="input" type="password" autocomplete="new-password" minlength="8" />
        <p v-if="nuError" class="err">{{ nuError }}</p>
      </form>
      <template #footer>
        <Btn variant="ghost" @click="creatingUser = false">Cancel</Btn>
        <Btn variant="primary" type="submit" form="nu-form" :loading="createUser.isPending.value">Create user</Btn>
      </template>
    </Modal>

    <Modal :open="creatingToken" title="New API token" :description="created ? 'Copy it now: it will not be shown again.' : 'For automation and scripts.'" @update:open="(o: boolean) => !o && closeToken()">
      <div v-if="created" class="tok">
        <div class="tok__val mono">{{ created.token }}</div>
        <Btn icon="copy" @click="copy(created.token)">Copy token</Btn>
        <p class="small muted">Use it as <code>Authorization: Bearer {{ created.prefix }}…</code></p>
      </div>
      <form v-else id="nt-form" class="form" @submit.prevent="ntError = ''; createToken.mutate()">
        <label class="label" for="nt-name">Name</label>
        <input id="nt-name" v-model="nt.name" class="input" required placeholder="ci-silencer" />
        <label class="label" for="nt-role">Role</label>
        <select id="nt-role" v-model="nt.role" class="select mono">
          <option v-for="r in roles" :key="r" :value="r">{{ r }}</option>
        </select>
        <label class="label" for="nt-exp">Expires</label>
        <select id="nt-exp" v-model="nt.expires_in" class="select mono">
          <option value="">never</option>
          <option value="24h">24 hours</option>
          <option value="720h">30 days</option>
          <option value="2160h">90 days</option>
          <option value="8760h">1 year</option>
        </select>
        <p v-if="ntError" class="err">{{ ntError }}</p>
      </form>
      <template #footer>
        <template v-if="created"><Btn variant="primary" @click="closeToken">Done</Btn></template>
        <template v-else>
          <Btn variant="ghost" @click="closeToken">Cancel</Btn>
          <Btn variant="primary" type="submit" form="nt-form" :loading="createToken.isPending.value">Create token</Btn>
        </template>
      </template>
    </Modal>
  </div>
</template>

<style scoped>
.atabs {
  display: flex;
  gap: 4px;
  border-bottom: 1px solid var(--border);
  margin-bottom: 14px;
}
.atab {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  border: 0;
  background: none;
  padding: 9px 12px;
  color: var(--text-dim);
  font-weight: 500;
  border-bottom: 2px solid transparent;
  margin-bottom: -1px;
}
.atab[data-state='active'] {
  color: var(--text-strong);
  border-bottom-color: var(--accent);
}
.bar {
  display: flex;
  justify-content: flex-end;
  margin-bottom: 10px;
}
.pad {
  padding: 16px;
}
.tbl-wrap {
  overflow-x: auto;
}
.u__name {
  color: var(--text-strong);
  font-weight: 500;
}
.you {
  font-size: 10px;
  color: var(--accent);
  border: 1px solid var(--accent-ring);
  border-radius: 3px;
  padding: 0 4px;
  margin-left: 4px;
}
.small {
  font-size: var(--fs-sm);
}
.off td {
  opacity: 0.55;
}
.role-sel {
  width: 130px;
  min-height: 26px;
  height: 26px;
  padding-top: 2px;
  padding-bottom: 2px;
  font-size: var(--fs-sm);
}
.right {
  text-align: right;
}
.switch {
  width: 34px;
  height: 20px;
  border-radius: 10px;
  border: 1px solid var(--border-strong);
  background: var(--bg-active);
  position: relative;
  padding: 0;
  transition: background var(--dur) var(--ease);
}
.switch[data-state='checked'] {
  background: var(--accent);
  border-color: var(--accent);
}
.switch:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.switch__thumb {
  display: block;
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background: var(--text-strong);
  transform: translateX(2px);
  transition: transform var(--dur) var(--ease);
}
.switch[data-state='checked'] .switch__thumb {
  transform: translateX(16px);
  background: var(--text-inverse);
}
.form {
  display: flex;
  flex-direction: column;
}
.form .label:not(:first-child) {
  margin-top: 12px;
}
.err {
  color: var(--danger);
  margin: 10px 0 0;
}
.tok {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 10px;
}
.tok__val {
  width: 100%;
  padding: 10px 12px;
  border-radius: var(--radius);
  background: var(--code-bg);
  border: 1px solid var(--accent-ring);
  color: var(--accent);
  overflow-wrap: anywhere;
  user-select: all;
}
</style>
