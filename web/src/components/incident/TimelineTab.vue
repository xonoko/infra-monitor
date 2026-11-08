<script setup lang="ts">
import { computed } from 'vue'
import type { TimelineEvent } from '@/api/types'
import Icon from '../ui/Icon.vue'
import Skeleton from '../ui/Skeleton.vue'
import EmptyState from '../ui/EmptyState.vue'
import Markdown from '../ui/Markdown.vue'
import { absTime, clockTime } from '@/lib/format'
import { useMe } from '@/composables/useMe'

const props = defineProps<{ events?: TimelineEvent[]; loading?: boolean }>()
const { traceURL } = useMe()

type Tone = 'critical' | 'resolved' | 'agent' | 'action' | 'user' | 'neutral' | 'warn'

const s = (v: unknown) => (typeof v === 'string' ? v : v == null ? '' : String(v))

function describe(e: TimelineEvent): { icon: string; tone: Tone; text: string; body?: string } {
  const p = e.payload ?? {}
  switch (e.type) {
    case 'incident_opened':
      return { icon: 'incidents', tone: 'critical', text: 'Incident opened' }
    case 'incident_reopened':
      return { icon: 'refresh', tone: 'critical', text: 'Incident reopened (fired again within the reopen window)' }
    case 'signal_firing':
      return { icon: 'bell', tone: 'critical', text: `Signal firing${p.name ? `: ${s(p.name)}` : ''}${p.fingerprint ? ` · ${s(p.fingerprint).slice(0, 8)}` : ''}` }
    case 'signal_resolved':
      return { icon: 'check', tone: 'resolved', text: `Signal resolved${p.name ? `: ${s(p.name)}` : ''}` }
    case 'incident_status_changed':
      return {
        icon: p.to === 'resolved' ? 'check' : 'chevronRight',
        tone: p.to === 'resolved' || p.to === 'closed' ? 'resolved' : p.to === 'open' ? 'critical' : 'warn',
        text: `Status ${s(p.from) || '—'} → ${s(p.to)}${p.reason ? ` · ${s(p.reason)}` : ''}`,
      }
    case 'investigation_started':
      return { icon: 'sparkle', tone: 'agent', text: `Investigation started${p.trigger ? ` (${s(p.trigger)})` : ''}` }
    case 'investigation_completed':
      return {
        icon: 'sparkle',
        tone: 'agent',
        text: `Investigation completed${p.verdict ? ` · verdict ${s(p.verdict)}` : ''}${typeof p.confidence === 'number' ? ` · ${Math.round(p.confidence * 100)}%` : ''}`,
        body: s(p.summary),
      }
    case 'investigation_failed':
      return { icon: 'info', tone: 'warn', text: `Investigation failed${p.error ? `: ${s(p.error)}` : ''}` }
    case 'investigation_skipped':
      return { icon: 'info', tone: 'neutral', text: `Investigation skipped${p.reason ? `: ${s(p.reason)}` : ''}` }
    case 'action_proposed':
      return { icon: 'bolt', tone: 'action', text: `Proposed ${s(p.summary) || s(p.type)}` }
    case 'action_approved':
      return { icon: 'check', tone: 'action', text: `Approved ${s(p.summary) || s(p.type)}` }
    case 'action_rejected':
      return { icon: 'x', tone: 'neutral', text: `Rejected ${s(p.summary) || s(p.type)}${p.reason ? ` · ${s(p.reason)}` : ''}` }
    case 'action_started':
      return { icon: 'play', tone: 'action', text: `Running ${s(p.summary) || s(p.type)}` }
    case 'action_succeeded':
      return { icon: 'check', tone: 'resolved', text: `Succeeded ${s(p.summary) || s(p.type)}`, body: p.output ? '```\n' + s(p.output).slice(0, 600) + '\n```' : undefined }
    case 'action_failed':
      return { icon: 'x', tone: 'critical', text: `Failed ${s(p.summary) || s(p.type)}${p.error ? `: ${s(p.error)}` : ''}` }
    case 'action_expired':
      return { icon: 'clock', tone: 'neutral', text: `Proposal expired: ${s(p.summary) || s(p.type)}` }
    case 'message':
      return { icon: 'message', tone: p.role === 'agent' ? 'agent' : 'user', text: p.role === 'agent' ? 'Agent replied' : 'Message', body: s(p.content) }
    case 'mute_created':
      return { icon: 'mute', tone: 'neutral', text: `Muted${p.ends_at ? ` until ${absTime(s(p.ends_at), true)}` : ''}${p.reason ? ` · ${s(p.reason)}` : ''}` }
    case 'mute_ended':
      return { icon: 'bell', tone: 'neutral', text: 'Mute ended' }
    case 'notification_routed':
      return { icon: 'send', tone: 'neutral', text: `Routed to ${(Array.isArray(p.channels) ? p.channels : []).join(', ') || '—'}${p.reason ? ` · ${s(p.reason)}` : ''}` }
    case 'notification_sent':
      return { icon: 'send', tone: 'neutral', text: `Notification sent${p.channel ? ` to ${s(p.channel)}` : ''}` }
    case 'assigned':
      return { icon: 'user', tone: 'user', text: `Assigned to ${s(p.assignee) || s(p.name) || '—'}` }
    case 'agent_thinking':
      return { icon: 'sparkle', tone: 'agent', text: 'Agent is working…' }
    default:
      return { icon: 'dot', tone: 'neutral', text: e.type.replace(/_/g, ' ') }
  }
}

function actor(e: TimelineEvent): string {
  if (e.actor_type === 'agent') return e.actor_name || 'agent'
  if (e.actor_type === 'system') return e.actor_name || 'system'
  const via = e.channel && e.channel !== 'system' ? ` via ${e.channel}` : ''
  return `${e.actor_name || 'someone'}${via}`
}

const groups = computed(() => {
  const out: { day: string; items: (TimelineEvent & { d: ReturnType<typeof describe> })[] }[] = []
  for (const e of props.events ?? []) {
    const day = new Date(e.created_at).toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric' })
    let g = out[out.length - 1]
    if (!g || g.day !== day) out.push((g = { day, items: [] }))
    g.items.push({ ...e, d: describe(e) })
  }
  return out
})
</script>

<template>
  <Skeleton v-if="loading" :lines="8" />
  <EmptyState v-else-if="!events?.length" icon="clock" title="No events yet" />
  <div v-else class="tl">
    <div v-for="g in groups" :key="g.day" class="tl__day">
      <div class="tl__dayhead section-title">{{ g.day }}</div>
      <ol class="tl__list">
        <li v-for="e in g.items" :key="e.id" class="ev" :class="`ev--${e.d.tone}`">
          <time class="ev__time mono" :datetime="e.created_at" :title="absTime(e.created_at)">{{ clockTime(e.created_at) }}</time>
          <span class="ev__node"><Icon :name="e.d.icon" :size="12" /></span>
          <div class="ev__main">
            <div class="ev__line">
              <span class="ev__text">{{ e.d.text }}</span>
              <span class="ev__actor mono" :class="`ev__actor--${e.actor_type}`">
                <Icon v-if="e.channel === 'telegram'" name="telegram" :size="11" />{{ actor(e) }}
              </span>
              <a v-if="e.trace_id && traceURL(e.trace_id)" class="ev__trace mono" :href="traceURL(e.trace_id)" target="_blank" rel="noopener" title="View trace">
                <Icon name="external" :size="11" />trace
              </a>
            </div>
            <div v-if="e.d.body" class="ev__body"><Markdown :source="e.d.body.length > 1200 ? e.d.body.slice(0, 1200) + '…' : e.d.body" /></div>
          </div>
        </li>
      </ol>
    </div>
  </div>
</template>

<style scoped>
.tl__day + .tl__day {
  margin-top: 18px;
}
.tl__dayhead {
  margin-bottom: 8px;
}
.tl__list {
  list-style: none;
  margin: 0;
  padding: 0;
  position: relative;
}
.ev {
  --c: var(--text-faint);
  display: grid;
  grid-template-columns: 64px 20px minmax(0, 1fr);
  gap: 0 8px;
  position: relative;
  padding-bottom: 10px;
}
.ev::before {
  content: '';
  position: absolute;
  left: calc(64px + 8px + 9.5px);
  top: 20px;
  bottom: -2px;
  width: 1px;
  background: var(--border);
}
.ev:last-child::before {
  display: none;
}
.ev--critical {
  --c: var(--sev-critical);
}
.ev--resolved {
  --c: var(--st-resolved);
}
.ev--agent {
  --c: var(--accent);
}
.ev--action {
  --c: var(--sev-warning);
}
.ev--warn {
  --c: var(--sev-warning);
}
.ev--user {
  --c: var(--sev-info);
}
.ev__time {
  color: var(--text-faint);
  font-size: var(--fs-xs);
  padding-top: 3px;
  text-align: right;
}
.ev__node {
  width: 20px;
  height: 20px;
  border-radius: 50%;
  display: grid;
  place-items: center;
  color: var(--c);
  background: color-mix(in srgb, var(--c) 14%, var(--bg-elev));
  border: 1px solid color-mix(in srgb, var(--c) 35%, transparent);
  position: relative;
  z-index: 1;
}
.ev__line {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 4px 10px;
  padding-top: 1px;
}
.ev__text {
  color: var(--text-strong);
  overflow-wrap: anywhere;
}
.ev__actor {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: var(--fs-xs);
  color: var(--text-dim);
  padding: 0 6px;
  border-radius: 4px;
  background: var(--bg-hover);
}
.ev__actor--agent {
  color: var(--accent);
  background: var(--accent-soft);
}
.ev__trace {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  font-size: var(--fs-xs);
}
.ev__body {
  margin-top: 6px;
  padding: 8px 10px;
  border-radius: var(--radius);
  border: 1px solid var(--border);
  background: var(--bg-panel);
  font-size: var(--fs-sm);
}
@media (max-width: 520px) {
  .ev {
    grid-template-columns: 46px 20px minmax(0, 1fr);
  }
  .ev::before {
    left: calc(46px + 8px + 9.5px);
  }
}
</style>
