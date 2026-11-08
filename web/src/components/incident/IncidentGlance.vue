<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import type { Action, IncidentDetail, Signal, TimelineEvent } from '@/api/types'
import Chip from '../ui/Chip.vue'
import Btn from '../ui/Btn.vue'
import CostTable from '../ui/CostTable.vue'
import Icon from '../ui/Icon.vue'
import { useMe } from '@/composables/useMe'
import { useActionDecisions } from '@/composables/useActionDecisions'
import { absTime, age, countdown, duration, relTime, sevKey, usd } from '@/lib/format'
import { useNow } from '@/lib/now'
import { hash8, revealAction } from '@/lib/approval'

const props = defineProps<{
  incident: IncidentDetail
  actions?: Action[]
  signals?: Signal[]
  timeline?: TimelineEvent[]
}>()
const emit = defineEmits<{ goto: [view: string]; propose: [] }>()

const route = useRoute()
const router = useRouter()
const now = useNow()
const { isOperator } = useMe()
const decisions = useActionDecisions(computed(() => props.incident.id))
const more = ref(false)

const pending = computed(() => (props.actions ?? []).filter((a) => a.status === 'proposed' && !a.superseded_by))
const firing = computed(() => (props.signals ?? []).filter((s) => s.status === 'firing'))
const total = computed(() => props.signals?.length ?? props.incident.signal_count)
const dur = computed(() => {
  const i = props.incident
  const end = i.resolved_at ? new Date(i.resolved_at).getTime() : now.value
  return duration((end - new Date(i.opened_at).getTime()) / 1000)
})
const members = computed(() => {
  const seen = new Set<number>()
  const out: { id: number; title: string; severity: string }[] = []
  for (const e of props.timeline ?? []) {
    if (e.type !== 'storm_member') continue
    const id = Number(e.payload.incident_id)
    if (!id || seen.has(id)) continue
    seen.add(id)
    out.push({ id, title: String(e.payload.title ?? `#${id}`), severity: String(e.payload.severity ?? '') })
  }
  return out
})
const showAllMembers = ref(false)

function openIncident(id: number) {
  if (route.name === 'incident') void router.push(`/incidents/${id}`)
  else void router.replace({ query: { ...route.query, incident: String(id) } })
}
const signalHint = (s: Signal) => s.labels.pod || s.labels.instance || s.labels.deployment || s.labels.service || s.labels.job || s.fingerprint.slice(0, 8)
</script>

<template>
  <aside class="gl" :class="{ 'gl--open': more }" aria-label="Incident at a glance">
    <!-- callouts: always visible -->
    <div v-if="incident.storm_id" class="callout callout--storm">
      <Icon name="incidents" :size="14" />
      <span>Part of alert storm <button class="link mono" @click="openIncident(incident.storm_id!)">#{{ incident.storm_id }}</button>. Investigated once at the storm level.</span>
    </div>
    <div v-if="incident.investigation_note" class="callout">
      <Icon name="info" :size="14" />
      <span><b>AI:</b> {{ incident.investigation_note }}</span>
    </div>

    <section v-if="incident.is_storm" class="gl__sec">
      <h3 class="gl__h">Grouped incidents <span class="mono faint">{{ members.length }}</span></h3>
      <p v-if="!members.length" class="faint small">No incident grouped yet.</p>
      <ul class="members">
        <li v-for="mm in showAllMembers ? members : members.slice(0, 8)" :key="mm.id">
          <button class="member" @click="openIncident(mm.id)">
            <span class="member__sev" :class="`member__sev--${sevKey(mm.severity)}`" />
            <span class="mono faint">#{{ mm.id }}</span>
            <span class="member__title">{{ mm.title }}</span>
          </button>
        </li>
      </ul>
      <button v-if="members.length > 8 && !showAllMembers" class="link small" @click="showAllMembers = true">Show all {{ members.length }}</button>
    </section>

    <!-- pending decisions: pinned -->
    <section v-if="pending.length" class="gl__sec gl__pending">
      <h3 class="gl__h gl__h--warn"><Icon name="bolt" :size="13" /> Awaiting decision <span class="mono">{{ pending.length }}</span></h3>
      <div v-for="a in pending" :key="a.id" class="pa">
        <div v-if="a.prepared" class="pa__sum mono">
          <span class="pa__op">{{ a.prepared.operation }}</span> {{ a.prepared.target }}
        </div>
        <div v-else class="pa__sum mono">{{ a.summary || a.type }}</div>
        <div class="pa__meta mono">
          <span v-if="a.prepared?.core_infrastructure" class="pa__core">core infra</span>
          <Chip kind="risk" :value="a.prepared?.risk ?? a.risk" dense />
          <span>{{ a.type }}</span>
          <span v-if="a.payload_hash" :title="a.payload_hash"><Icon name="key" :size="10" /> {{ hash8(a.payload_hash) }}</span>
          <span :class="{ 'pa__exp--soon': new Date(a.expires_at).getTime() - now < 5 * 60e3 }"><Icon name="clock" :size="10" /> {{ countdown(a.expires_at, now) }}</span>
        </div>
        <div v-if="a.prepared?.argo_managed" class="pa__note">Argo-managed: a live change will be reverted</div>
        <div v-if="a.prepared && !a.prepared.dry_run_ok" class="pa__note pa__note--bad">Dry-run failed: can't be approved</div>
        <div class="pa__btns">
          <Btn
            size="sm"
            variant="primary"
            icon="check"
            :disabled="!isOperator || (!!a.prepared && !a.prepared.dry_run_ok)"
            :loading="decisions.busyFor(a) === 'approve'"
            @click="decisions.approve(a)"
            >{{ a.prepared ? 'Review…' : 'Approve' }}</Btn
          >
          <Btn size="sm" variant="danger" icon="x" :disabled="!isOperator" :loading="decisions.busyFor(a) === 'reject'" @click="decisions.reject(a)">Reject</Btn>
          <button v-if="a.prepared" class="link small" @click="revealAction(a.id)">See payload</button>
        </div>
      </div>
    </section>

    <!-- narrow screens: the rest folds behind this toggle -->
    <button class="gl__toggle" :aria-expanded="more" @click="more = !more">
      <Icon :name="more ? 'chevronDown' : 'chevronRight'" :size="13" />
      <span>Details</span>
      <span class="mono faint">{{ firing.length }}/{{ total }} firing · {{ dur }}<template v-if="incident.cost?.total?.calls"> · AI {{ usd(incident.cost.total.usd, incident.cost.total.calls) }}</template></span>
    </button>

    <div class="gl__more">
      <section class="gl__sec">
        <dl class="facts">
          <dt>Status</dt>
          <dd><Chip kind="status" :value="incident.status" dense /></dd>
          <dt>Severity</dt>
          <dd><Chip kind="severity" :value="incident.severity || 'none'" dense /></dd>
          <dt>Verdict</dt>
          <dd><Chip kind="verdict" :value="incident.verdict" dense /></dd>
          <dt>Where</dt>
          <dd class="mono">{{ incident.cluster || '—' }}<template v-if="incident.namespace"> / {{ incident.namespace }}</template></dd>
          <dt>{{ incident.resolved_at ? 'Lasted' : 'Open for' }}</dt>
          <dd class="mono" :title="`opened ${absTime(incident.opened_at)}`">{{ dur }}</dd>
          <dt>Last signal</dt>
          <dd class="mono">{{ relTime(incident.last_signal_at, now) }}</dd>
          <dt>Notified</dt>
          <dd class="mono">
            <template v-if="incident.notify_channels?.length">
              <span v-for="c in incident.notify_channels" :key="c" class="chan"><Icon name="telegram" :size="11" />{{ c }}</span>
            </template>
            <span v-else class="faint">{{ incident.muted ? 'muted' : 'UI only' }}</span>
          </dd>
          <template v-if="incident.assignee">
            <dt>Assignee</dt>
            <dd>{{ incident.assignee.name }}</dd>
          </template>
        </dl>
      </section>

      <section class="gl__sec">
        <h3 class="gl__h"><Icon name="sparkle" :size="12" /> AI cost</h3>
        <CostTable :cost="incident.cost" />
      </section>

      <section class="gl__sec">
        <h3 class="gl__h">
          Firing signals <span class="mono" :class="firing.length ? 'hot' : 'faint'">{{ firing.length }}/{{ total }}</span>
          <button class="link small gl__hlink" @click="emit('goto', 'signals')">All signals →</button>
        </h3>
        <p v-if="!firing.length" class="faint small">Nothing firing.</p>
        <ul class="sigs">
          <li v-for="s in firing.slice(0, 5)" :key="s.id" class="sig" :title="s.annotations.summary || s.name">
            <span class="sig__dot" :class="`sig__dot--${sevKey(s.severity)}`" />
            <span class="sig__name">{{ s.name }}</span>
            <span class="sig__hint mono">{{ signalHint(s) }}</span>
            <span class="sig__age mono">{{ age(s.starts_at, now) }}</span>
          </li>
        </ul>
        <button v-if="firing.length > 5" class="link small" @click="emit('goto', 'signals')">+{{ firing.length - 5 }} more</button>
      </section>

      <section v-if="isOperator" class="gl__sec">
        <Btn size="sm" variant="subtle" icon="plus" @click="emit('propose')">Propose an action</Btn>
      </section>
    </div>
  </aside>
</template>

<style scoped>
.gl {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 12px 14px;
  font-size: var(--fs-sm);
}
.gl__sec {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.gl__h {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: var(--fs-xs);
  text-transform: uppercase;
  letter-spacing: 0.07em;
  color: var(--text-dim);
  font-weight: 600;
  margin: 0;
}
.gl__h--warn {
  color: var(--sev-warning);
}
.gl__hlink {
  margin-left: auto;
  text-transform: none;
  letter-spacing: 0;
  font-weight: 500;
}
.small {
  font-size: var(--fs-xs);
}
.hot {
  color: var(--sev-critical);
}
.link {
  border: 0;
  background: none;
  padding: 0;
  color: var(--accent);
  cursor: pointer;
  font: inherit;
}
.link:hover {
  text-decoration: underline;
}
.callout {
  display: flex;
  gap: 8px;
  align-items: flex-start;
  padding: 8px 10px;
  border-radius: var(--radius);
  background: var(--sev-warning-soft);
  border: 1px solid color-mix(in srgb, var(--sev-warning) 30%, transparent);
  color: var(--text);
}
.callout > :first-child {
  color: var(--sev-warning);
  margin-top: 2px;
  flex: none;
}
.callout b {
  color: var(--text-strong);
}
.callout--storm {
  background: var(--sev-info-soft);
  border-color: color-mix(in srgb, var(--sev-info) 30%, transparent);
}
.callout--storm > :first-child {
  color: var(--sev-info);
}

.members {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.member {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 100%;
  border: 0;
  background: none;
  padding: 3px 4px;
  border-radius: 4px;
  text-align: left;
  color: var(--text);
  min-width: 0;
}
.member:hover {
  background: var(--bg-hover);
}
.member__sev,
.sig__dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  flex: none;
  background: var(--sev-other);
}
.member__sev--critical,
.sig__dot--critical {
  background: var(--sev-critical);
}
.member__sev--warning,
.sig__dot--warning {
  background: var(--sev-warning);
}
.member__sev--info,
.sig__dot--info {
  background: var(--sev-info);
}
.member__title {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.gl__pending {
  padding: 10px;
  border-radius: var(--radius-lg);
  border: 1px solid color-mix(in srgb, var(--sev-warning) 40%, var(--border));
  background: color-mix(in srgb, var(--sev-warning) 5%, var(--bg-panel));
}
.pa + .pa {
  border-top: 1px solid var(--border);
  padding-top: 8px;
}
.pa__sum {
  color: var(--text-strong);
  font-weight: 500;
  overflow-wrap: anywhere;
}
.pa__meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px 10px;
  margin-top: 4px;
  font-size: var(--fs-xs);
  color: var(--text-dim);
}
.pa__op {
  color: var(--sev-warning);
}
.pa__core {
  color: var(--text-inverse);
  background: var(--danger);
  border-radius: 3px;
  padding: 0 5px;
  font-weight: 600;
}
.pa__note {
  margin-top: 4px;
  font-size: var(--fs-xs);
  color: var(--sev-warning);
}
.pa__note--bad {
  color: var(--danger);
  font-weight: 600;
}
.pa__btns {
  align-items: center;
}
.pa__exp--soon {
  color: var(--sev-critical);
}
.pa__btns {
  display: flex;
  gap: 6px;
  margin-top: 8px;
}

.gl__toggle {
  display: none;
  align-items: center;
  gap: 6px;
  border: 0;
  background: none;
  padding: 2px 0;
  color: var(--text);
  font-weight: 500;
  text-align: left;
}
.gl__more {
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.facts {
  display: grid;
  grid-template-columns: 82px 1fr;
  gap: 6px 8px;
  margin: 0;
}
.facts dt {
  color: var(--text-faint);
}
.facts dd {
  margin: 0;
  min-width: 0;
  overflow-wrap: anywhere;
}
.chan {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  margin-right: 6px;
}
.sigs {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 3px;
}
.sig {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
.sig__name {
  color: var(--text-strong);
  white-space: nowrap;
}
.sig__hint {
  font-size: var(--fs-xs);
  color: var(--text-dim);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex: 1;
  min-width: 0;
}
.sig__age {
  font-size: var(--fs-xs);
  color: var(--text-faint);
}

/* Narrow incident view (drawer, phone): the panel sits above the stream and folds. */
@container incident (max-width: 899px) {
  .gl {
    padding: 8px 14px;
    gap: 8px;
  }
  .gl__toggle {
    display: flex;
  }
  .gl:not(.gl--open) .gl__more {
    display: none;
  }
  .gl__more {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
    gap: 12px 20px;
    padding-bottom: 4px;
  }
}
</style>
