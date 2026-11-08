<script setup lang="ts">
import { computed, ref } from 'vue'
import type { IncidentDetail } from '@/api/types'
import type { useIncidentData } from '@/composables/useIncident'
import Chip from '../ui/Chip.vue'
import Icon from '../ui/Icon.vue'
import Markdown from '../ui/Markdown.vue'
import LabelList from '../ui/LabelList.vue'
import Skeleton from '../ui/Skeleton.vue'
import Btn from '../ui/Btn.vue'
import { absTime, duration, pct, relTime } from '@/lib/format'
import { useMe } from '@/composables/useMe'
import { progressFor } from '@/lib/progress'

const props = defineProps<{ incident: IncidentDetail; data: ReturnType<typeof useIncidentData> }>()
const emit = defineEmits<{ goto: [tab: string] }>()
const { traceURL } = useMe()

const inv = computed(() => props.incident.latest_investigation ?? props.data.investigations.data.value?.[0] ?? null)
const showFull = ref(false)
const pending = computed(() => (props.data.actions.data.value ?? []).filter((a) => a.status === 'proposed'))
const working = computed(() => props.incident.investigation_state === 'running' || props.incident.investigation_state === 'pending')
const progress = computed(() => progressFor(props.incident.id))
const invDuration = computed(() => {
  const i = inv.value
  if (!i?.started_at || !i.finished_at) return ''
  return duration((new Date(i.finished_at).getTime() - new Date(i.started_at).getTime()) / 1000)
})
const confColor = computed(() => {
  const c = inv.value?.confidence ?? 0
  return c >= 0.75 ? 'var(--success)' : c >= 0.45 ? 'var(--warning)' : 'var(--sev-other)'
})
</script>

<template>
  <div class="ov">
    <!-- AI investigation -->
    <section class="card ai">
      <div class="card__head">
        <span class="ai__badge"><Icon name="sparkle" :size="14" />AI investigation</span>
        <template v-if="inv">
          <Chip kind="action" :value="inv.status" dense />
          <span class="mono faint small">{{ inv.agent }}{{ inv.model ? ` · ${inv.model}` : '' }}</span>
        </template>
        <span class="spacer" />
        <a v-if="inv?.trace_id && traceURL(inv.trace_id)" :href="traceURL(inv.trace_id)" target="_blank" rel="noopener" class="small trace-link">
          <Icon name="external" :size="12" /> trace
        </a>
      </div>

      <div v-if="working" class="ai__working">
        <span class="dots" aria-hidden="true"><i /><i /><i /></span>
        <span>Investigating…</span>
        <span v-if="progress.items.length" class="mono faint small ai__last">
          {{ progress.items[progress.items.length - 1].tool ?? progress.items[progress.items.length - 1].text }}
        </span>
      </div>

      <template v-if="inv && (inv.status === 'succeeded' || inv.summary)">
        <div class="ai__verdict">
          <div class="kv">
            <span class="kv__k">Verdict</span>
            <Chip kind="verdict" :value="inv.verdict" />
          </div>
          <div class="kv">
            <span class="kv__k">Confidence</span>
            <span class="conf">
              <span class="conf__bar"><span :style="{ width: pct(inv.confidence), background: confColor }" /></span>
              <span class="mono">{{ pct(inv.confidence) }}</span>
            </span>
          </div>
          <div v-if="invDuration" class="kv">
            <span class="kv__k">Took</span>
            <span class="mono">{{ invDuration }}</span>
          </div>
          <div v-if="inv.prompt_tokens" class="kv">
            <span class="kv__k">Tokens</span>
            <span class="mono">{{ (inv.prompt_tokens + inv.completion_tokens).toLocaleString() }}</span>
          </div>
        </div>
        <p v-if="inv.summary" class="ai__summary">{{ inv.summary }}</p>
        <div v-if="inv.root_cause" class="ai__rc">
          <div class="section-title">Root cause</div>
          <Markdown :source="inv.root_cause" />
        </div>
        <div v-if="inv.analysis" class="ai__full">
          <button class="ai__toggle" :aria-expanded="showFull" @click="showFull = !showFull">
            <Icon :name="showFull ? 'chevronDown' : 'chevronRight'" :size="13" /> Full analysis
          </button>
          <div v-if="showFull" class="ai__analysis"><Markdown :source="inv.analysis" /></div>
        </div>
      </template>
      <div v-else-if="inv && (inv.status === 'failed' || inv.status === 'timed_out')" class="ai__failed">
        <Icon name="info" :size="14" />
        <div>
          <b>AI investigation unavailable.</b>
          <span class="muted"> {{ inv.error || (inv.status === 'timed_out' ? 'The agent did not answer in time.' : 'The agent failed.') }} Notifications were sent with the raw alert.</span>
        </div>
      </div>
      <p v-else-if="!inv && !working" class="muted small">
        {{ incident.investigation_state === 'skipped' ? 'Investigation was skipped (rate limit, mute or cooldown).' : 'No investigation yet.' }}
      </p>
      <Skeleton v-if="!inv && data.investigations.isPending.value" :lines="3" />
    </section>

    <!-- pending proposals -->
    <section v-if="pending.length" class="card prop">
      <div class="card__head">
        <span class="prop__badge"><Icon name="bolt" :size="14" />{{ pending.length }} action{{ pending.length > 1 ? 's' : '' }} waiting for approval</span>
        <span class="spacer" />
        <Btn size="sm" variant="subtle" @click="emit('goto', 'actions')">Review</Btn>
      </div>
      <ul class="prop__list">
        <li v-for="a in pending" :key="a.id" class="mono">
          <Chip kind="risk" :value="a.risk" dense /> <span>{{ a.summary || a.type }}</span>
        </li>
      </ul>
    </section>

    <div class="grid">
      <section class="card">
        <div class="card__head"><span class="section-title">Details</span></div>
        <dl class="dl">
          <dt>Status</dt>
          <dd><Chip kind="status" :value="incident.status" dense /></dd>
          <dt>Opened</dt>
          <dd class="mono" :title="absTime(incident.opened_at)">{{ absTime(incident.opened_at, true) }} <span class="faint">({{ relTime(incident.opened_at) }})</span></dd>
          <template v-if="incident.acknowledged_at">
            <dt>Acknowledged</dt>
            <dd class="mono">{{ absTime(incident.acknowledged_at, true) }}</dd>
          </template>
          <template v-if="incident.resolved_at">
            <dt>Resolved</dt>
            <dd class="mono">{{ absTime(incident.resolved_at, true) }}</dd>
          </template>
          <dt>Last signal</dt>
          <dd class="mono">{{ relTime(incident.last_signal_at) }}</dd>
          <dt>Signals</dt>
          <dd class="mono">{{ incident.firing_count }} firing / {{ incident.signal_count }}</dd>
          <dt>Assignee</dt>
          <dd>{{ incident.assignee?.name ?? '—' }}</dd>
          <dt>Notified</dt>
          <dd class="mono">
            <template v-if="incident.notify_channels?.length">
              <span v-for="c in incident.notify_channels" :key="c" class="chan"><Icon name="telegram" :size="12" />{{ c }}</span>
            </template>
            <span v-else class="faint">{{ incident.muted ? 'muted' : 'UI only' }}</span>
          </dd>
          <template v-if="incident.trace_id && traceURL(incident.trace_id)">
            <dt>Trace</dt>
            <dd><a :href="traceURL(incident.trace_id)" target="_blank" rel="noopener" class="mono small">{{ incident.trace_id.slice(0, 16) }}…</a></dd>
          </template>
        </dl>
      </section>
      <section class="card">
        <div class="card__head"><span class="section-title">Labels</span></div>
        <LabelList :labels="incident.labels" />
      </section>
    </div>
  </div>
</template>

<style scoped>
.ov {
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.card {
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  background: var(--bg-panel);
  padding: 12px 14px;
}
.card__head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 10px;
  flex-wrap: wrap;
}
.spacer {
  flex: 1;
}
.small {
  font-size: var(--fs-xs);
}
.trace-link {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}
.ai {
  border-color: color-mix(in srgb, var(--accent) 28%, var(--border));
  background: linear-gradient(180deg, color-mix(in srgb, var(--accent) 5%, var(--bg-panel)), var(--bg-panel) 60%);
}
.ai__badge,
.prop__badge {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-weight: 600;
  color: var(--accent);
  font-size: var(--fs-sm);
}
.prop__badge {
  color: var(--sev-warning);
}
.ai__working {
  display: flex;
  align-items: center;
  gap: 10px;
  color: var(--text);
  margin-bottom: 8px;
  min-width: 0;
}
.ai__last {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.dots {
  display: inline-flex;
  gap: 3px;
}
.dots i {
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: var(--accent);
  animation: blink 1.2s infinite ease-in-out;
}
.dots i:nth-child(2) {
  animation-delay: 0.15s;
}
.dots i:nth-child(3) {
  animation-delay: 0.3s;
}
@keyframes blink {
  0%,
  80%,
  100% {
    opacity: 0.2;
  }
  40% {
    opacity: 1;
  }
}
.ai__verdict {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 22px;
  margin-bottom: 10px;
}
.kv {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.kv__k {
  font-size: var(--fs-xs);
  color: var(--text-faint);
  text-transform: uppercase;
  letter-spacing: 0.06em;
}
.conf {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-size: var(--fs-sm);
}
.conf__bar {
  width: 80px;
  height: 5px;
  border-radius: 3px;
  background: var(--bg-active);
  overflow: hidden;
}
.conf__bar span {
  display: block;
  height: 100%;
  border-radius: 3px;
}
.ai__summary {
  margin: 0 0 10px;
  color: var(--text-strong);
  font-size: var(--fs-lg);
  line-height: 1.5;
}
.ai__rc {
  margin-bottom: 8px;
}
.ai__rc .section-title {
  margin-bottom: 4px;
}
.ai__toggle {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  border: 0;
  background: none;
  color: var(--text-dim);
  padding: 4px 0;
  font-weight: 500;
}
.ai__toggle:hover {
  color: var(--text-strong);
}
.ai__analysis {
  margin-top: 6px;
  padding: 12px;
  background: var(--bg-elev);
  border: 1px solid var(--border);
  border-radius: var(--radius);
}
.ai__failed {
  display: flex;
  gap: 8px;
  color: var(--sev-warning);
  background: var(--sev-warning-soft);
  border-radius: var(--radius);
  padding: 8px 10px;
}
.ai__failed b {
  color: var(--text-strong);
}
.prop {
  border-color: color-mix(in srgb, var(--sev-warning) 35%, var(--border));
}
.prop__list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: var(--fs-sm);
}
.prop__list li {
  display: flex;
  align-items: center;
  gap: 8px;
}
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
  gap: 14px;
}
.dl {
  display: grid;
  grid-template-columns: 110px 1fr;
  gap: 7px 10px;
  margin: 0;
  font-size: var(--fs-sm);
}
.dl dt {
  color: var(--text-faint);
}
.dl dd {
  margin: 0;
  min-width: 0;
  overflow-wrap: anywhere;
}
.chan {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  margin-right: 8px;
}
</style>
