<script setup lang="ts">
import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { api, errorMessage } from '@/api/client'
import { qk } from '@/api/keys'
import type { HeartbeatStatus } from '@/api/types'
import Skeleton from '@/components/ui/Skeleton.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Icon from '@/components/ui/Icon.vue'
import Btn from '@/components/ui/Btn.vue'
import CostTable from '@/components/ui/CostTable.vue'
import { absTime, duration, relTime, usd } from '@/lib/format'
import { useNow } from '@/lib/now'

const now = useNow()
const st = useQuery({ queryKey: qk.status, queryFn: api.status, refetchInterval: 15_000 })
const s = computed(() => st.data.value)

const leases = computed(() => (s.value?.leases ?? []).map((l) => ({ name: l, held: true })))
const hbTone = (h: HeartbeatStatus) => (h.state === 'ok' ? 'ok' : h.state === 'silent' ? 'bad' : 'unknown')
const sourceTone = (src: { last_received_at?: string | null; last_error?: string; last_error_at?: string | null }) => {
  if (src.last_error && (!src.last_received_at || (src.last_error_at && src.last_error_at > src.last_received_at))) return 'bad'
  if (!src.last_received_at) return 'unknown'
  return 'ok'
}
const capList = (c?: Record<string, unknown>) =>
  Object.entries(c ?? {})
    .filter(([, v]) => v === true)
    .map(([k]) => k.replace(/([A-Z])/g, ' $1').toLowerCase().trim())
</script>

<template>
  <div class="page">
    <header class="page-header">
      <div>
        <h1>Status</h1>
        <p>
          Sources, channels and heartbeats.
          <span v-if="s" class="mono faint">{{ s.version }} · replica {{ s.replica }}</span>
        </p>
      </div>
      <Btn variant="ghost" icon="refresh" :loading="st.isFetching.value" @click="st.refetch()">Refresh</Btn>
    </header>

    <Skeleton v-if="st.isPending.value" :lines="8" :height="20" />
    <EmptyState v-else-if="st.isError.value" icon="info" title="Could not load status" :text="errorMessage(st.error.value)" />
    <div v-else-if="s" class="grid">
      <section class="panel box">
        <h2 class="box__title"><Icon name="heart" :size="15" /> Heartbeats</h2>
        <p v-if="!s.heartbeats?.length" class="muted">No heartbeat received yet. The Watchdog alert is used as a dead man's switch.</p>
        <ul class="rows">
          <li v-for="h in s.heartbeats" :key="h.key" class="row">
            <span class="dot" :class="`dot--${hbTone(h)}`" />
            <div class="row__main">
              <div class="row__title mono">{{ h.key }}</div>
              <div class="row__sub mono">
                last seen <span :title="absTime(h.last_seen_at)">{{ relTime(h.last_seen_at, now) }}</span>
                <template v-if="h.expected_interval_seconds"> · every {{ duration(h.expected_interval_seconds) }}</template>
              </div>
              <div v-if="h.last_ping_error" class="row__err mono">ping: {{ h.last_ping_error }}</div>
              <div v-else-if="h.last_ping_at" class="row__sub mono">external ping {{ relTime(h.last_ping_at, now) }}</div>
            </div>
            <span class="state mono" :class="`state--${hbTone(h)}`">{{ h.state }}</span>
          </li>
        </ul>
      </section>

      <section class="panel box">
        <h2 class="box__title"><Icon name="database" :size="15" /> Sources</h2>
        <p v-if="!s.sources?.length" class="muted">No source configured.</p>
        <ul class="rows">
          <li v-for="src in s.sources" :key="src.name" class="row">
            <span class="dot" :class="`dot--${sourceTone(src)}`" />
            <div class="row__main">
              <div class="row__title"><span class="mono">{{ src.name }}</span> <span class="tag mono">{{ src.type }}</span></div>
              <div class="row__sub mono">
                last webhook {{ relTime(src.last_received_at, now) }}
                <template v-if="src.last_reconciled_at"> · reconciled {{ relTime(src.last_reconciled_at, now) }}</template>
                <template v-if="src.received_total !== undefined"> · {{ src.received_total.toLocaleString() }} received</template>
              </div>
              <div v-if="src.last_error" class="row__err mono">{{ src.last_error }} <span class="faint">({{ relTime(src.last_error_at, now) }})</span></div>
            </div>
          </li>
        </ul>
      </section>

      <section class="panel box">
        <h2 class="box__title"><Icon name="send" :size="15" /> Channels</h2>
        <p v-if="!s.channels?.length" class="muted">No channel configured: incidents stay in the UI.</p>
        <ul class="rows">
          <li v-for="c in s.channels" :key="c.name" class="row">
            <span class="dot" :class="c.last_error ? 'dot--bad' : c.connected === false ? 'dot--unknown' : 'dot--ok'" />
            <div class="row__main">
              <div class="row__title">
                <span class="mono">{{ c.name }}</span> <span class="tag mono">{{ c.type }}</span>
                <span v-if="c.mode" class="tag mono">{{ c.mode }}</span>
                <span v-if="c.leader" class="tag tag--accent mono" title="This replica runs the receiver">receiver here</span>
              </div>
              <div v-if="capList(c.capabilities).length" class="row__sub">{{ capList(c.capabilities).join(' · ') }}</div>
              <div v-if="c.last_error" class="row__err mono">{{ c.last_error }}</div>
            </div>
          </li>
        </ul>
      </section>

      <section class="panel box">
        <h2 class="box__title"><Icon name="sparkle" :size="15" /> Agent</h2>
        <dl class="dl mono">
          <dt>enabled</dt>
          <dd>
            <span class="state" :class="s.agent?.enabled ? 'state--ok' : 'state--unknown'">{{ s.agent?.enabled ? 'yes' : 'no' }}</span>
          </dd>
          <template v-if="s.agent?.type"><dt>type</dt><dd>{{ s.agent.type }}</dd></template>
          <template v-if="s.agent?.model"><dt>model</dt><dd>{{ s.agent.model }}</dd></template>
          <template v-if="s.agent?.url_host"><dt>endpoint</dt><dd>{{ s.agent.url_host }}</dd></template>
          <template v-if="s.agent?.max_concurrent"><dt>concurrency</dt><dd>{{ s.agent.max_concurrent }}</dd></template>
          <template v-if="s.agent?.max_per_hour"><dt>calls / hour</dt><dd>{{ s.agent.calls_last_hour ?? 0 }} / {{ s.agent.max_per_hour }}</dd></template>
          <template v-if="s.agent?.max_per_day"><dt>calls / 24h</dt><dd>{{ s.agent.calls_today ?? 0 }} / {{ s.agent.max_per_day }}</dd></template>
          <template v-if="s.agent?.deep_model"><dt>deep model</dt><dd>{{ s.agent.deep_model }}</dd></template>
          <template v-if="s.agent?.storm_threshold"><dt>storm mode</dt><dd>&gt; {{ s.agent.storm_threshold }} incidents / {{ Math.round((s.agent.storm_window_seconds ?? 0) / 60) }}m</dd></template>
          <template v-if="s.agent?.budget_paused_until"><dt>budget</dt><dd><span class="state state--silent">paused until {{ new Date(s.agent.budget_paused_until).toLocaleTimeString() }}</span></dd></template>
        </dl>
        <template v-if="s.agent?.enabled || s.agent?.cost_last_24h">
          <h3 class="box__sub section-title cost-h">
            Cost (24h)
            <span class="mono cost-h__total">{{ usd(s.agent?.cost_last_24h?.total?.usd, s.agent?.cost_last_24h?.total?.calls ?? 0) }}</span>
          </h3>
          <CostTable :cost="s.agent?.cost_last_24h" />
        </template>
        <template v-if="leases.length">
          <h3 class="box__sub section-title">Leases on this replica</h3>
          <ul class="leases mono">
            <li v-for="l in leases" :key="l.name"><span class="dot" :class="l.held ? 'dot--ok' : 'dot--unknown'" /> {{ l.name }}</li>
          </ul>
        </template>
      </section>
    </div>
  </div>
</template>

<style scoped>
.cost-h {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
}
.cost-h__total {
  font-size: var(--fs-md);
  color: var(--text-strong);
  text-transform: none;
  letter-spacing: 0;
}
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(360px, 1fr));
  gap: 16px;
}
.box {
  padding: 14px 16px;
}
.box__title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: var(--fs-md);
  margin-bottom: 12px;
}
.box__sub {
  margin: 16px 0 8px;
}
.rows {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
}
.row {
  display: flex;
  gap: 10px;
  align-items: flex-start;
  padding: 9px 0;
  border-top: 1px solid var(--border);
}
.row:first-child {
  border-top: 0;
  padding-top: 0;
}
.row__main {
  flex: 1;
  min-width: 0;
}
.row__title {
  color: var(--text-strong);
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: center;
}
.row__sub {
  font-size: var(--fs-xs);
  color: var(--text-dim);
  margin-top: 3px;
}
.row__err {
  font-size: var(--fs-xs);
  color: var(--danger);
  margin-top: 3px;
  overflow-wrap: anywhere;
}
.dot {
  width: 9px;
  height: 9px;
  border-radius: 50%;
  margin-top: 5px;
  flex: none;
  background: var(--text-faint);
}
.dot--ok {
  background: var(--success);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--success) 18%, transparent);
}
.dot--bad {
  background: var(--danger);
  box-shadow: 0 0 0 3px var(--danger-soft);
}
.tag {
  font-size: 10px;
  padding: 0 5px;
  border-radius: 3px;
  border: 1px solid var(--border-strong);
  color: var(--text-dim);
}
.tag--accent {
  color: var(--accent);
  border-color: var(--accent-ring);
}
.state {
  font-size: var(--fs-xs);
  text-transform: uppercase;
  letter-spacing: 0.05em;
  font-weight: 600;
}
.state--ok {
  color: var(--success);
}
.state--bad {
  color: var(--danger);
}
.state--unknown {
  color: var(--text-faint);
}
.dl {
  display: grid;
  grid-template-columns: 110px 1fr;
  gap: 6px 10px;
  margin: 0;
  font-size: var(--fs-sm);
}
.dl dt {
  color: var(--text-faint);
}
.dl dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.leases {
  list-style: none;
  margin: 0;
  padding: 0;
  font-size: var(--fs-sm);
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.leases li {
  display: flex;
  align-items: center;
  gap: 8px;
}
.leases .dot {
  margin-top: 0;
}
@media (max-width: 520px) {
  .grid {
    grid-template-columns: 1fr;
  }
}
</style>
