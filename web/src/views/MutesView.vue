<script setup lang="ts">
import { computed, ref } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { api, errorMessage } from '@/api/client'
import { qk } from '@/api/keys'
import type { Matcher, MatcherOp } from '@/api/types'
import Btn from '@/components/ui/Btn.vue'
import Modal from '@/components/ui/Modal.vue'
import Skeleton from '@/components/ui/Skeleton.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Icon from '@/components/ui/Icon.vue'
import { absTime, countdown, relTime } from '@/lib/format'
import { useNow } from '@/lib/now'
import { useMe } from '@/composables/useMe'
import { toastError, toastOk } from '@/lib/toast'

const qc = useQueryClient()
const now = useNow()
const { isOperator } = useMe()
const show = ref<'active' | 'all'>('active')
const mutes = useQuery({ queryKey: computed(() => [...qk.mutes, show.value]), queryFn: () => api.mutes(show.value === 'active' ? true : undefined).then((r) => r.items) })

const expire = useMutation({
  mutationFn: (id: number) => api.expireMute(id),
  onSuccess: () => {
    toastOk('Mute expired')
    qc.invalidateQueries({ queryKey: qk.mutes })
  },
  onError: (e) => toastError('Could not expire mute', e),
})

// create
const creating = ref(false)
const ops: MatcherOp[] = ['=', '!=', '=~', '!~']
const matchers = ref<Matcher[]>([{ name: 'alertname', op: '=', value: '' }])
const durationStr = ref('2h')
const reason = ref('')
const mirror = ref(false)
const formError = ref('')
const create = useMutation({
  mutationFn: () =>
    api.createMute({
      matchers: matchers.value.filter((m) => m.name && m.value !== undefined),
      duration: durationStr.value,
      reason: reason.value,
      mirror_to_alertmanager: mirror.value || undefined,
    }),
  onSuccess: () => {
    toastOk('Mute created')
    creating.value = false
    matchers.value = [{ name: 'alertname', op: '=', value: '' }]
    reason.value = ''
    qc.invalidateQueries({ queryKey: qk.mutes })
    qc.invalidateQueries({ queryKey: qk.incidents() })
  },
  onError: (e) => (formError.value = errorMessage(e)),
})
function submit() {
  formError.value = ''
  if (!matchers.value.some((m) => m.name.trim())) {
    formError.value = 'Add at least one matcher'
    return
  }
  create.mutate()
}
const isActive = (m: { starts_at: string; ends_at: string; active: boolean }) => m.active
</script>

<template>
  <div class="page">
    <header class="page-header">
      <div>
        <h1>Mutes</h1>
        <p>Muted incidents are still recorded but not notified or auto-investigated.</p>
      </div>
      <div class="hdr-actions">
        <div class="seg" role="radiogroup" aria-label="Show">
          <button role="radio" :aria-checked="show === 'active'" :class="{ on: show === 'active' }" @click="show = 'active'">Active</button>
          <button role="radio" :aria-checked="show === 'all'" :class="{ on: show === 'all' }" @click="show = 'all'">All</button>
        </div>
        <Btn variant="primary" icon="plus" :disabled="!isOperator" @click="creating = true">New mute</Btn>
      </div>
    </header>

    <div class="panel">
      <div v-if="mutes.isPending.value" style="padding: 16px"><Skeleton :lines="5" /></div>
      <EmptyState v-else-if="mutes.isError.value" icon="info" title="Could not load mutes" :text="errorMessage(mutes.error.value)" />
      <EmptyState v-else-if="!mutes.data.value?.length" icon="mute" title="No mutes" :text="show === 'active' ? 'Nothing is muted right now.' : 'No mutes yet.'" />
      <div v-else class="tbl-wrap">
        <table class="table">
          <thead>
            <tr>
              <th>Matchers</th>
              <th>Window</th>
              <th>Reason</th>
              <th>By</th>
              <th />
            </tr>
          </thead>
          <tbody>
            <tr v-for="m in mutes.data.value" :key="m.id" :class="{ dim: !isActive(m) }">
              <td>
                <div class="matchers mono">
                  <span v-for="(x, i) in m.matchers" :key="i" class="matcher">{{ x.name }}<b>{{ x.op }}</b>"{{ x.value }}"</span>
                </div>
                <div v-if="m.incident_id" class="sub mono"><RouterLink :to="{ path: '/incidents', query: { incident: String(m.incident_id) } }">from #{{ m.incident_id }}</RouterLink></div>
              </td>
              <td class="mono nowrap">
                <span v-if="isActive(m)" class="live"><Icon name="clock" :size="11" /> {{ countdown(m.ends_at, now) }}</span>
                <span v-else class="faint">ended {{ relTime(m.ends_at, now) }}</span>
                <div class="sub" :title="`${absTime(m.starts_at)} → ${absTime(m.ends_at)}`">{{ absTime(m.starts_at, true) }} → {{ absTime(m.ends_at, true) }}</div>
              </td>
              <td class="reason">{{ m.reason || '—' }}<div v-if="m.alertmanager_silence_id" class="sub mono">AM silence {{ m.alertmanager_silence_id.slice(0, 8) }}</div></td>
              <td class="nowrap">{{ m.created_by_name || '—' }}</td>
              <td class="right"><Btn v-if="isActive(m)" size="sm" variant="ghost" :disabled="!isOperator" :loading="expire.isPending.value && expire.variables.value === m.id" @click="expire.mutate(m.id)">Expire</Btn></td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <Modal v-model:open="creating" title="New mute" description="Incidents whose labels match all matchers are muted." :width="560">
      <form id="mute-form" class="mf" @submit.prevent="submit">
        <span class="label">Matchers</span>
        <div v-for="(m, i) in matchers" :key="i" class="mf__row">
          <input v-model="m.name" class="input mono" placeholder="label" aria-label="Label name" />
          <select v-model="m.op" class="select mono" aria-label="Operator">
            <option v-for="o in ops" :key="o" :value="o">{{ o }}</option>
          </select>
          <input v-model="m.value" class="input mono" placeholder="value" aria-label="Value" />
          <Btn variant="ghost" size="sm" icon="trash" aria-label="Remove matcher" :disabled="matchers.length === 1" @click="matchers.splice(i, 1)" />
        </div>
        <Btn size="sm" variant="subtle" icon="plus" class="mf__add" @click="matchers.push({ name: '', op: '=', value: '' })">Add matcher</Btn>
        <div class="mf__grid">
          <div>
            <label class="label" for="mf-dur">Duration</label>
            <select id="mf-dur" v-model="durationStr" class="select mono">
              <option v-for="d in ['30m', '1h', '2h', '4h', '8h', '24h', '72h', '168h']" :key="d" :value="d">{{ d }}</option>
            </select>
          </div>
          <label class="mf__check"><input v-model="mirror" type="checkbox" /> Also create an Alertmanager silence</label>
        </div>
        <label class="label" for="mf-reason">Reason</label>
        <input id="mf-reason" v-model="reason" class="input" placeholder="Maintenance window, known issue…" />
        <p v-if="formError" class="err">{{ formError }}</p>
      </form>
      <template #footer>
        <Btn variant="ghost" @click="creating = false">Cancel</Btn>
        <Btn variant="primary" type="submit" form="mute-form" :loading="create.isPending.value">Create mute</Btn>
      </template>
    </Modal>
  </div>
</template>

<style scoped>
.hdr-actions {
  display: flex;
  gap: 8px;
  align-items: center;
}
.seg {
  display: inline-flex;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius);
  overflow: hidden;
}
.seg button {
  border: 0;
  background: transparent;
  color: var(--text-dim);
  padding: 0 12px;
  height: 28px;
}
.seg button.on {
  background: var(--bg-active);
  color: var(--text-strong);
}
.tbl-wrap {
  overflow-x: auto;
}
.matchers {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  font-size: var(--fs-sm);
}
.matcher {
  background: var(--bg-hover);
  border: 1px solid var(--border);
  border-radius: 4px;
  padding: 0 6px;
}
.matcher b {
  color: var(--accent);
  font-weight: 500;
  margin: 0 1px;
}
.sub {
  font-size: var(--fs-xs);
  color: var(--text-faint);
  margin-top: 3px;
}
.live {
  color: var(--sev-warning);
  display: inline-flex;
  align-items: center;
  gap: 4px;
}
.dim td {
  color: var(--text-dim);
}
.nowrap {
  white-space: nowrap;
}
.reason {
  max-width: 320px;
}
.right {
  text-align: right;
}
.mf {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.mf__row {
  display: grid;
  grid-template-columns: 1fr 70px 1.3fr auto;
  gap: 6px;
  align-items: center;
}
.mf__add {
  align-self: flex-start;
}
.mf__grid {
  display: grid;
  grid-template-columns: 140px 1fr;
  gap: 12px;
  align-items: end;
  margin-top: 8px;
}
.mf__check {
  display: flex;
  gap: 8px;
  align-items: center;
  padding-bottom: 7px;
}
.err {
  color: var(--danger);
  margin: 4px 0 0;
}
@media (max-width: 520px) {
  .mf__row {
    grid-template-columns: 1fr 60px 1fr auto;
  }
  .mf__grid {
    grid-template-columns: 1fr;
  }
}
</style>
