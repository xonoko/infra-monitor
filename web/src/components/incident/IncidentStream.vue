<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import type { Action, IncidentDetail, Investigation, Message, TimelineEvent } from '@/api/types'
import ActionCard from './ActionCard.vue'
import InvestigationCard from './InvestigationCard.vue'
import Btn from '../ui/Btn.vue'
import Icon from '../ui/Icon.vue'
import Markdown from '../ui/Markdown.vue'
import Skeleton from '../ui/Skeleton.vue'
import { useIncidentMutations } from '@/composables/useIncident'
import { useActionDecisions } from '@/composables/useActionDecisions'
import { useMe } from '@/composables/useMe'
import { markThinking, progressFor } from '@/lib/progress'
import { absTime, clockTime, hhmm } from '@/lib/format'
import { describeEvent, originLabel, type Described } from '@/lib/timeline'
import { streamFilter } from '@/lib/streamFilter'

const props = defineProps<{
  incident: IncidentDetail
  timeline?: TimelineEvent[]
  messages?: Message[]
  actions?: Action[]
  investigations?: Investigation[]
  loading?: boolean
}>()

const id = computed(() => props.incident.id)
const m = useIncidentMutations(id)
const decisions = useActionDecisions(id)
const { isOperator, traceURL } = useMe()
const progress = computed(() => progressFor(props.incident.id))

// ---------- entries ----------
interface Base {
  key: string
  at: number
  seq: number
}
type Entry =
  | (Base & { kind: 'message'; msg: Message; ev?: TimelineEvent })
  | (Base & { kind: 'investigation'; inv?: Investigation; ev?: TimelineEvent })
  | (Base & { kind: 'action'; action: Action; ev?: TimelineEvent })
  | (Base & { kind: 'note'; ev: TimelineEvent; d: Described })
  | (Base & { kind: 'system'; ev: TimelineEvent; d: Described })
type Row =
  | Entry
  | { kind: 'group'; key: string; items: (Entry & { kind: 'system' })[] }
  | { kind: 'day'; key: string; label: string }

const t = (s: string) => new Date(s).getTime()
const num = (v: unknown) => (typeof v === 'number' ? v : typeof v === 'string' && v ? Number(v) : NaN)

const entries = computed<Entry[]>(() => {
  const msgs = new Map((props.messages ?? []).map((x) => [x.id, x]))
  const acts = new Map((props.actions ?? []).map((x) => [x.id, x]))
  const invs = props.investigations ?? []
  const invById = new Map(invs.map((x) => [x.id, x]))
  const seenMsg = new Set<number>()
  const seenAct = new Set<number>()
  const seenInv = new Set<number>()
  const out: Entry[] = []
  let seq = 0

  const events = [...(props.timeline ?? [])].sort((a, b) => a.id - b.id)
  for (const ev of events) {
    const p = ev.payload ?? {}
    const base = { at: t(ev.created_at), seq: seq++ }
    switch (ev.type) {
      case 'agent_thinking':
        continue
      case 'message': {
        const mid = num(p.message_id)
        const msg: Message = msgs.get(mid) ?? {
          id: Number.isFinite(mid) ? mid : -ev.id,
          incident_id: ev.incident_id,
          role: (p.role as Message['role']) ?? (ev.actor_type === 'agent' ? 'agent' : 'user'),
          author_name: ev.actor_name,
          channel: ev.channel,
          channel_name: ev.channel_name,
          content: String(p.content ?? ''),
          status: 'done',
          action_ids: [],
          created_at: ev.created_at,
        }
        if (seenMsg.has(msg.id)) continue
        seenMsg.add(msg.id)
        out.push({ ...base, kind: 'message', key: `m${msg.id}`, msg, ev })
        continue
      }
      case 'investigation_completed': {
        let inv = invById.get(num(p.investigation_id))
        if (!inv) {
          // closest finished investigation not yet shown
          inv = invs
            .filter((x) => !seenInv.has(x.id) && x.finished_at && (x.status === 'succeeded' || x.summary))
            .sort((a, b) => Math.abs(t(a.finished_at!) - base.at) - Math.abs(t(b.finished_at!) - base.at))[0]
        }
        if (inv) seenInv.add(inv.id)
        out.push({ ...base, kind: 'investigation', key: `i${ev.id}`, inv, ev })
        continue
      }
      case 'investigation_failed':
      case 'investigation_skipped':
        out.push({ ...base, kind: 'note', key: `e${ev.id}`, ev, d: describeEvent(ev) })
        continue
      case 'action_proposed': {
        const a = acts.get(num(p.action_id))
        if (a && !seenAct.has(a.id)) {
          seenAct.add(a.id)
          out.push({ ...base, kind: 'action', key: `a${a.id}`, action: a, ev })
          continue
        }
        break
      }
      case 'action_approved':
      case 'action_rejected':
      case 'action_started':
      case 'action_succeeded':
      case 'action_failed':
      case 'action_expired':
        // folded into the action card, which updates in place
        if (acts.has(num(p.action_id))) continue
        break
    }
    out.push({ ...base, kind: 'system', key: `e${ev.id}`, ev, d: describeEvent(ev) })
  }
  // rows that have no timeline event (e.g. agent refusals, older data)
  for (const msg of props.messages ?? []) {
    if (!seenMsg.has(msg.id)) out.push({ kind: 'message', key: `m${msg.id}`, msg, at: t(msg.created_at), seq: seq++ })
  }
  for (const a of props.actions ?? []) {
    if (!seenAct.has(a.id)) out.push({ kind: 'action', key: `a${a.id}`, action: a, at: t(a.created_at), seq: seq++ })
  }
  for (const inv of invs) {
    if (!seenInv.has(inv.id) && inv.status === 'succeeded') out.push({ kind: 'investigation', key: `iv${inv.id}`, inv, at: t(inv.finished_at ?? inv.created_at), seq: seq++ })
  }
  return out.sort((a, b) => a.at - b.at || a.seq - b.seq)
})

const visible = computed(() => (streamFilter.value === 'conversation' ? entries.value.filter((e) => e.kind !== 'system') : entries.value))

function dayLabel(ms: number) {
  const d = new Date(ms)
  const today = new Date()
  const y = new Date(Date.now() - 86400e3)
  if (d.toDateString() === today.toDateString()) return 'Today'
  if (d.toDateString() === y.toDateString()) return 'Yesterday'
  return d.toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric' })
}

const rows = computed<Row[]>(() => {
  const out: Row[] = []
  let day = ''
  const list = visible.value
  for (let i = 0; i < list.length; i++) {
    const e = list[i]
    const dl = dayLabel(e.at)
    if (dl !== day) {
      day = dl
      out.push({ kind: 'day', key: `d${e.key}`, label: dl })
    }
    if (e.kind === 'system') {
      // collapse consecutive identical system events (flapping alerts, repeated notifications)
      const run: (Entry & { kind: 'system' })[] = [e]
      while (i + 1 < list.length) {
        const n = list[i + 1]
        if (n.kind !== 'system' || n.d.group !== e.d.group || dayLabel(n.at) !== day) break
        run.push(n)
        i++
      }
      if (run.length > 1) {
        out.push({ kind: 'group', key: `g${e.key}`, items: run })
        continue
      }
    }
    out.push(e)
  }
  return out
})
const expanded = ref<Record<string, boolean>>({})

// ---------- scrolling ----------
const scroller = ref<HTMLElement>()
const atBottom = ref(true)
const unseen = ref(0)
const pendingAgent = computed(() => (props.messages ?? []).some((x) => x.role === 'agent' && x.status === 'pending'))
const working = computed(() => progress.value.active || pendingAgent.value)

function onScroll() {
  const el = scroller.value
  if (!el) return
  atBottom.value = el.scrollTop + el.clientHeight >= el.scrollHeight - 48
  if (atBottom.value) unseen.value = 0
}
function toBottom(smooth = false) {
  void nextTick(() => {
    const el = scroller.value
    if (!el) return
    el.scrollTo({ top: el.scrollHeight, behavior: smooth ? 'smooth' : 'auto' })
    unseen.value = 0
    atBottom.value = true
  })
}
watch(
  () => entries.value.length,
  (n, old) => {
    if (!old || atBottom.value) toBottom()
    else if (n > old) unseen.value += n - old
  },
)
watch(
  () => [progress.value.items.length, working.value],
  () => {
    if (atBottom.value) toBottom()
  },
)
watch(streamFilter, () => toBottom())
watch(id, () => {
  unseen.value = 0
  atBottom.value = true
  toBottom()
})
onMounted(() => toBottom())

// ---------- composer ----------
const draft = ref('')
const input = ref<HTMLTextAreaElement>()
function send() {
  const content = draft.value.trim()
  if (!content || m.send.isPending.value) return
  m.send.mutate(content, {
    onSuccess: () => {
      draft.value = ''
      markThinking(props.incident.id)
      toBottom(true)
    },
  })
}
function onKey(e: KeyboardEvent) {
  if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
    e.preventDefault()
    send()
  }
}
defineExpose({ focusComposer: () => input.value?.focus() })

// ---------- helpers ----------
function author(msg: Message) {
  if (msg.role === 'agent') return msg.author_name || 'Agent'
  if (msg.role === 'system') return 'infra-monitor'
  return msg.author_name || 'someone'
}
function origin(msg: Message) {
  if (msg.role === 'agent') return (msg.author_name || 'agent').toLowerCase() === 'agent' ? '' : 'agent'
  if (msg.role === 'system') return 'system'
  return `via ${msg.channel && msg.channel !== 'system' ? msg.channel : 'ui'}`
}
function groupText(items: { ev: TimelineEvent; d: Described }[]) {
  if (items[0].ev.type === 'storm_member') return `Grouped  incidents into this storm`
  return items[0].d.text
}
const initial = (s: string) => (s.trim()[0] ?? '?').toUpperCase()
const evOrigin = (ev: TimelineEvent) => originLabel(ev.actor_type, ev.actor_name, ev.channel)
const suggestions = ['What changed right before this started?', 'Show the logs of the failing pods', 'Is this related to other open incidents?']
</script>

<template>
  <div class="st">
    <div ref="scroller" class="st__log" role="log" aria-live="polite" aria-label="Incident activity" @scroll.passive="onScroll">
      <Skeleton v-if="loading" :lines="8" />
      <template v-else>
        <div v-if="!rows.length && !working" class="st__empty">
          <p class="muted">{{ streamFilter === 'conversation' ? 'No conversation yet.' : 'No activity yet.' }} Ask the agent about this incident; replies in the Telegram thread land here too.</p>
          <div v-if="isOperator" class="st__sugg">
            <button v-for="s in suggestions" :key="s" class="st__suggbtn" @click="draft = s; input?.focus()">{{ s }}</button>
          </div>
        </div>

        <template v-for="row in rows" :key="row.key">
          <!-- day divider -->
          <div v-if="row.kind === 'day'" class="day"><span>{{ row.label }}</span></div>

          <!-- chat message -->
          <article
            v-else-if="row.kind === 'message'"
            class="msg"
            :class="[`msg--${row.msg.role}`, { 'msg--failed': row.msg.status === 'failed' }]"
          >
            <div class="msg__av" aria-hidden="true">
              <Icon v-if="row.msg.role === 'agent'" name="sparkle" :size="14" />
              <Icon v-else-if="row.msg.role === 'system'" name="info" :size="14" />
              <template v-else>{{ initial(author(row.msg)) }}</template>
            </div>
            <div class="msg__main">
              <header class="msg__head">
                <span class="msg__who">{{ author(row.msg) }}</span>
                <span v-if="origin(row.msg)" class="msg__via mono"><Icon v-if="row.msg.channel === 'telegram'" name="telegram" :size="11" />{{ origin(row.msg) }}</span>
                <time class="msg__time mono" :datetime="row.msg.created_at" :title="absTime(row.msg.created_at)">{{ clockTime(row.msg.created_at) }}</time>
                <span v-if="row.msg.status === 'pending'" class="msg__state mono">sending…</span>
                <a v-if="row.ev?.trace_id && traceURL(row.ev.trace_id)" :href="traceURL(row.ev.trace_id)" target="_blank" rel="noopener" class="msg__trace mono" title="View trace"><Icon name="external" :size="10" />trace</a>
              </header>
              <div class="msg__body"><Markdown :source="row.msg.content" /></div>
            </div>
          </article>

          <!-- investigation result -->
          <div v-else-if="row.kind === 'investigation'" class="card-row">
            <div class="card-row__meta mono">
              <time :title="absTime(row.ev?.created_at ?? row.inv?.finished_at)">{{ hhmm(row.ev?.created_at ?? row.inv?.finished_at ?? row.inv!.created_at) }}</time>
              <span>agent</span>
            </div>
            <InvestigationCard
              :inv="row.inv"
              :fallback="{ verdict: row.ev?.payload.verdict as string, confidence: row.ev?.payload.confidence as number, summary: row.ev?.payload.summary as string }"
            />
          </div>

          <!-- action proposal, updated in place -->
          <div v-else-if="row.kind === 'action'" class="card-row">
            <div class="card-row__meta mono">
              <time :title="absTime(row.action.created_at)">{{ hhmm(row.action.created_at) }}</time>
              <span>{{ row.ev ? evOrigin(row.ev) : row.action.proposed_by }}</span>
            </div>
            <ActionCard
              :action="row.action"
              :can-decide="isOperator"
              :busy="decisions.busyFor(row.action)"
              @approve="decisions.approve(row.action)"
              @reject="(r?: string) => decisions.reject(row.action, r)"
            />
          </div>

          <!-- investigation failed / skipped note -->
          <div v-else-if="row.kind === 'note'" class="note-row">
            <time class="sys__time mono" :title="absTime(row.ev.created_at)">{{ hhmm(row.ev.created_at) }}</time>
            <div class="note" :class="`note--${row.d.tone}`">
              <Icon :name="row.d.icon" :size="13" />
              <span class="note__text"><b>{{ row.d.text }}</b><template v-if="row.d.body"> — {{ row.d.body }}</template></span>
            </div>
          </div>

          <!-- collapsed run of identical system events -->
          <div v-else-if="row.kind === 'group'" class="sys-group">
            <button class="sys sys--btn" :class="`sys--${row.items[0].d.tone}`" :aria-expanded="!!expanded[row.key]" @click="expanded[row.key] = !expanded[row.key]">
              <time class="sys__time mono" :title="absTime(row.items[0].ev.created_at)">{{ hhmm(row.items[0].ev.created_at) }}</time>
              <span class="sys__node"><Icon :name="row.items[0].d.icon" :size="11" /></span>
              <span class="sys__text">{{ groupText(row.items) }}</span>
              <span class="sys__count mono">×{{ row.items.length }}</span>
              <span class="sys__span mono faint">until {{ clockTime(row.items[row.items.length - 1].ev.created_at) }}</span>
              <Icon :name="expanded[row.key] ? 'chevronDown' : 'chevronRight'" :size="12" class="faint" />
            </button>
            <div v-if="expanded[row.key]" class="sys-group__items">
              <div v-for="it in row.items" :key="it.key" class="sys sys--sub">
                <time class="sys__time mono">{{ clockTime(it.ev.created_at) }}</time>
                <span class="sys__text">{{ it.d.text }}</span>
                <span class="sys__by mono">{{ evOrigin(it.ev) }}</span>
              </div>
            </div>
          </div>

          <!-- single system event -->
          <div v-else-if="row.kind === 'system'" class="sys" :class="`sys--${row.d.tone}`">
            <time class="sys__time mono" :datetime="row.ev.created_at" :title="absTime(row.ev.created_at)">{{ hhmm(row.ev.created_at) }}</time>
            <span class="sys__node"><Icon :name="row.d.icon" :size="11" /></span>
            <span class="sys__text">{{ row.d.text }}<template v-if="row.d.body"> — {{ row.d.body }}</template></span>
            <span class="sys__by mono"><Icon v-if="row.ev.channel === 'telegram'" name="telegram" :size="10" />{{ evOrigin(row.ev) }}</span>
            <a v-if="row.ev.trace_id && traceURL(row.ev.trace_id)" :href="traceURL(row.ev.trace_id)" target="_blank" rel="noopener" class="sys__trace mono" title="View trace"><Icon name="external" :size="10" /></a>
          </div>
        </template>

        <!-- live agent progress -->
        <div v-if="working" class="live" role="status">
          <div class="live__head"><span class="dots" aria-hidden="true"><i /><i /><i /></span> Agent is working</div>
          <ol v-if="progress.items.length" class="live__list mono">
            <li v-for="(p, i) in progress.items" :key="i">
              <Icon :name="p.kind === 'tool_call' ? 'terminal' : p.kind === 'tool_result' ? 'check' : 'message'" :size="11" />
              <span v-if="p.tool" class="live__tool">{{ p.tool }}</span>
              <span v-if="p.text" class="live__text">{{ p.text }}</span>
            </li>
          </ol>
        </div>
      </template>
    </div>

    <button v-if="unseen > 0 && !atBottom" class="pill" @click="toBottom(true)">
      {{ unseen }} new {{ unseen === 1 ? 'entry' : 'entries' }} <Icon name="arrowUp" :size="12" class="pill__ico" />
    </button>

    <form class="composer" @submit.prevent="send">
      <textarea
        ref="input"
        v-model="draft"
        class="composer__input"
        rows="1"
        :placeholder="isOperator ? 'Message the agent and the incident thread… (Enter to send)' : 'Operator role required to chat'"
        :disabled="!isOperator"
        aria-label="Message the agent"
        @keydown="onKey"
      />
      <Btn variant="primary" icon="send" type="submit" :disabled="!isOperator || !draft.trim()" :loading="m.send.isPending.value" aria-label="Send" />
    </form>
  </div>
</template>

<style scoped>
.st {
  position: relative;
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
}
.st__log {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 10px 16px 14px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.st__empty {
  margin: auto;
  max-width: 420px;
  text-align: center;
}
.st__sugg {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-top: 12px;
}
.st__suggbtn {
  border: 1px solid var(--border-strong);
  background: var(--bg-panel);
  color: var(--text);
  border-radius: var(--radius);
  padding: 7px 10px;
  text-align: left;
}
.st__suggbtn:hover {
  border-color: var(--accent);
  color: var(--text-strong);
}

.day {
  display: flex;
  align-items: center;
  gap: 10px;
  margin: 8px 0 2px;
  font-size: var(--fs-xs);
  color: var(--text-faint);
  text-transform: uppercase;
  letter-spacing: 0.08em;
  font-weight: 600;
}
.day::before,
.day::after {
  content: '';
  flex: 1;
  height: 1px;
  background: var(--border);
}

/* chat messages */
.msg {
  display: grid;
  grid-template-columns: 44px minmax(0, 1fr);
  gap: 8px;
  padding: 6px 0;
  margin-top: 4px;
}
.msg__av {
  justify-self: center;
  width: 28px;
  height: 28px;
  border-radius: 50%;
  display: grid;
  place-items: center;
  background: var(--bg-active);
  color: var(--text-strong);
  font-size: var(--fs-sm);
  font-weight: 600;
}
.msg--agent .msg__av {
  background: var(--accent-soft);
  color: var(--accent);
}
.msg--system .msg__av {
  background: var(--sev-warning-soft);
  color: var(--sev-warning);
}
.msg__main {
  min-width: 0;
  max-width: 760px;
}
.msg__head {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: 2px 8px;
  margin-bottom: 3px;
}
.msg__who {
  font-weight: 600;
  color: var(--text-strong);
}
.msg--agent .msg__who {
  color: var(--accent);
}
.msg__via {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  font-size: var(--fs-xs);
  color: var(--text-dim);
}
.msg__time,
.msg__state {
  font-size: var(--fs-xs);
  color: var(--text-faint);
}
.msg__trace {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  font-size: var(--fs-xs);
}
.msg__body {
  padding: 8px 12px;
  border-radius: 4px var(--radius-lg) var(--radius-lg) var(--radius-lg);
  background: var(--bg-panel);
  border: 1px solid var(--border);
}
.msg--agent .msg__body {
  border-color: color-mix(in srgb, var(--accent) 24%, var(--border));
  background: linear-gradient(180deg, color-mix(in srgb, var(--accent) 6%, var(--bg-panel)), var(--bg-panel));
}
.msg--system .msg__body,
.msg--failed .msg__body {
  border-style: dashed;
  border-color: color-mix(in srgb, var(--sev-warning) 45%, var(--border));
  background: var(--sev-warning-soft);
  font-size: var(--fs-sm);
}

/* rich cards */
.card-row {
  display: grid;
  grid-template-columns: 44px minmax(0, 1fr);
  gap: 8px;
  margin: 6px 0;
}
.card-row__meta {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 2px;
  font-size: var(--fs-xs);
  color: var(--text-faint);
  padding-top: 10px;
  overflow: hidden;
}
.card-row__meta span {
  display: none;
}
.card-row > :last-child {
  max-width: 760px;
}

/* notes */
.note-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 2px 0;
}
.note {
  --c: var(--sev-warning);
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: baseline;
  gap: 8px;
  padding: 6px 10px;
  border-radius: var(--radius);
  color: var(--c);
  background: color-mix(in srgb, var(--c) 10%, transparent);
  border: 1px solid color-mix(in srgb, var(--c) 25%, transparent);
  font-size: var(--fs-sm);
  max-width: 722px;
}
.note--neutral {
  --c: var(--text-dim);
}
.note__text {
  color: var(--text);
}
.note__text b {
  color: var(--text-strong);
  font-weight: 600;
}

/* compact system lines */
.sys {
  --c: var(--text-faint);
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 22px;
  font-size: var(--fs-sm);
  color: var(--text-dim);
  min-width: 0;
}
.sys--critical {
  --c: var(--sev-critical);
}
.sys--resolved {
  --c: var(--st-resolved);
}
.sys--agent {
  --c: var(--accent);
}
.sys--action,
.sys--warn {
  --c: var(--sev-warning);
}
.sys--user {
  --c: var(--sev-info);
}
.sys__time {
  width: 44px;
  flex: none;
  font-size: var(--fs-xs);
  color: var(--text-faint);
  text-align: center;
}
.sys__node {
  width: 18px;
  height: 18px;
  border-radius: 50%;
  flex: none;
  display: grid;
  place-items: center;
  color: var(--c);
  background: color-mix(in srgb, var(--c) 12%, transparent);
}
.sys__text {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--text);
}
.sys__by {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  flex: none;
  font-size: var(--fs-xs);
  color: var(--text-faint);
}
.sys__trace {
  flex: none;
  display: inline-grid;
  color: var(--text-faint);
}
.sys__trace:hover {
  color: var(--accent);
}
.sys__count {
  flex: none;
  font-size: var(--fs-xs);
  color: var(--c);
  background: color-mix(in srgb, var(--c) 12%, transparent);
  border-radius: 8px;
  padding: 0 6px;
  font-weight: 600;
}
.sys__span {
  font-size: var(--fs-xs);
  flex: none;
}
.sys--btn {
  border: 0;
  background: none;
  padding: 0;
  width: 100%;
  text-align: left;
  font: inherit;
  font-size: var(--fs-sm);
  border-radius: 4px;
}
.sys--btn:hover {
  background: var(--bg-hover);
}
.sys-group__items {
  margin: 2px 0 4px 52px;
  padding-left: 10px;
  border-left: 1px solid var(--border);
}
.sys--sub {
  min-height: 20px;
  font-size: var(--fs-xs);
}
.sys--sub .sys__time {
  width: auto;
}

/* live progress */
.live {
  margin: 6px 0 0 52px;
  border: 1px dashed color-mix(in srgb, var(--accent) 40%, var(--border));
  border-radius: var(--radius-lg);
  padding: 8px 12px;
  background: color-mix(in srgb, var(--accent) 4%, transparent);
  max-width: 722px;
}
.live__head {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--accent);
  font-weight: 500;
  font-size: var(--fs-sm);
}
.live__list {
  list-style: none;
  margin: 8px 0 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 3px;
  font-size: var(--fs-xs);
  color: var(--text-dim);
}
.live__list li {
  display: flex;
  gap: 6px;
  align-items: baseline;
  min-width: 0;
}
.live__tool {
  color: var(--text-strong);
  white-space: nowrap;
}
.live__text {
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

.pill {
  position: absolute;
  left: 50%;
  bottom: 66px;
  transform: translateX(-50%);
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 28px;
  padding: 0 12px;
  border-radius: 14px;
  border: 1px solid var(--accent);
  background: var(--bg-elev);
  color: var(--accent);
  font-size: var(--fs-sm);
  font-weight: 500;
  box-shadow: var(--shadow-pop);
  z-index: 2;
}
.pill__ico {
  transform: rotate(180deg);
}

.composer {
  display: flex;
  gap: 8px;
  align-items: flex-end;
  padding: 10px 14px;
  border-top: 1px solid var(--border);
  background: var(--bg-elev);
  flex: none;
}
.composer__input {
  flex: 1;
  resize: none;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius);
  background: var(--bg-input);
  padding: 6px 10px;
  color: var(--text-strong);
  outline: none;
  line-height: 1.45;
  min-height: 32px;
  max-height: 160px;
  field-sizing: content;
}
.composer__input:focus {
  border-color: var(--border-focus);
  box-shadow: 0 0 0 3px var(--accent-ring);
}
.composer__input:disabled {
  opacity: 0.6;
}
@media (max-width: 520px) {
  .st__log {
    padding: 8px 10px 12px;
  }
  .sys__by {
    display: none;
  }
}
</style>
