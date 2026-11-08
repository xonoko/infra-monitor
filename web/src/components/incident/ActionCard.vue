<script setup lang="ts">
import { computed, ref } from 'vue'
import type { Action } from '@/api/types'
import Chip from '../ui/Chip.vue'
import Btn from '../ui/Btn.vue'
import Icon from '../ui/Icon.vue'
import Markdown from '../ui/Markdown.vue'
import CodeBlock from '../ui/CodeBlock.vue'
import { absTime, countdown, relTime } from '@/lib/format'
import { useNow } from '@/lib/now'
import { hash8, highlightedAction, revealAction } from '@/lib/approval'
import { toast, toastError } from '@/lib/toast'

const props = defineProps<{ action: Action; canDecide: boolean; busy?: 'approve' | 'reject' | null; rollbackBusy?: boolean }>()
const emit = defineEmits<{ approve: []; reject: [reason?: string]; rollback: [] }>()
const now = useNow()

const p = computed(() => props.action.prepared ?? null)
const pending = computed(() => props.action.status === 'proposed' && !props.action.superseded_by)
const expiresSoon = computed(() => new Date(props.action.expires_at).getTime() - now.value < 5 * 60e3)
const rejecting = ref(false)
const reason = ref('')
const showOutput = ref(false)
const params = computed(() => JSON.stringify(props.action.params ?? {}, null, 2))
const risk = computed(() => p.value?.risk ?? props.action.risk)
const dryRunBlocked = computed(() => !!p.value && !p.value.dry_run_ok)
const argoApp = computed(() => {
  const a = p.value?.argo_managed
  if (!a) return ''
  return typeof a === 'string' ? a : 'unknown'
})
// diffs stay expanded up to ~60 lines, other formats collapse earlier
const payloadOpenUpTo = computed(() => (p.value?.payload_format === 'diff' ? 60 : 30))
const canRollback = computed(() => props.action.status === 'succeeded' && !!p.value?.rollback_action)

async function copyHash() {
  const h = props.action.payload_hash
  if (!h) return
  try {
    await navigator.clipboard.writeText(h)
    toast('Payload hash copied', { description: h })
  } catch {
    toastError('Copy failed', h)
  }
}
</script>

<template>
  <article
    class="ac"
    :data-action-id="action.id"
    :class="{ 'ac--pending': pending, 'ac--core': p?.core_infrastructure && pending, 'ac--flash': highlightedAction === action.id, 'ac--superseded': !!action.superseded_by }"
  >
    <header class="ac__head">
      <Icon name="bolt" :size="14" class="ac__ico" />
      <span v-if="p" class="ac__summary"><span class="ac__op">{{ p.operation }}</span> {{ p.target }}</span>
      <span v-else class="ac__summary">{{ action.summary || action.type }}</span>
      <span v-if="p?.core_infrastructure" class="ac__core mono">Core infrastructure</span>
      <Chip kind="risk" :value="`${risk} risk`" dense />
      <Chip kind="action" :value="action.superseded_by ? 'superseded' : action.status" dense />
    </header>

    <div class="ac__meta mono">
      <span>#{{ action.id }} · {{ action.type }}</span>
      <span>proposed by {{ action.proposed_by }} · {{ relTime(action.created_at, now) }}</span>
      <span v-if="pending" :class="{ 'ac__exp--soon': expiresSoon }" :title="`expires ${absTime(action.expires_at)}`"><Icon name="clock" :size="11" /> {{ countdown(action.expires_at, now) }}</span>
      <span v-else-if="action.decided_by_name">
        {{ action.status === 'rejected' ? 'rejected' : 'approved' }} by {{ action.decided_by_name }}<template v-if="action.decided_via"> via {{ action.decided_via }}</template>
      </span>
      <button v-if="action.payload_hash" class="ac__hash" :title="`Payload sha256 ${action.payload_hash} — click to copy`" @click="copyHash">
        <Icon name="key" :size="11" />{{ hash8(action.payload_hash) }}
      </button>
    </div>

    <div v-if="action.superseded_by" class="ac__line ac__line--info">
      <Icon name="refresh" :size="13" />
      <span>Replaced by <button class="ac__link mono" @click="revealAction(action.superseded_by!)">#{{ action.superseded_by }}</button> (target changed). Review the updated proposal.</span>
    </div>

    <div v-if="argoApp" class="ac__line ac__line--warn">
      <Icon name="info" :size="13" />
      <span>
        Managed by Argo app <b class="mono">{{ argoApp }}</b>: a live change will be reverted by the next sync. Prefer the GitOps proposal.
      </span>
    </div>
    <ul v-if="p?.warnings?.length" class="ac__warns">
      <li v-for="(w, i) in p.warnings" :key="i"><Icon name="info" :size="12" />{{ w }}</li>
    </ul>

    <div v-if="action.rationale" class="ac__why"><Markdown :source="action.rationale" /></div>

    <template v-if="p">
      <dl class="ac__dl">
        <dt>Target</dt>
        <dd class="mono">{{ p.target }}</dd>
        <dt>Operation</dt>
        <dd class="mono">{{ p.operation }}</dd>
        <template v-if="p.effect"><dt>Effect</dt><dd>{{ p.effect }}</dd></template>
        <template v-if="p.precondition"><dt>Precondition</dt><dd class="mono">{{ p.precondition }}</dd></template>
        <template v-if="p.rollback"><dt>Rollback</dt><dd>{{ p.rollback }}</dd></template>
      </dl>

      <CodeBlock class="ac__code" :text="p.payload" :format="p.payload_format" :open-up-to="payloadOpenUpTo" :label="`Payload (${p.payload_format})`" />

      <section class="ac__dry" :class="p.dry_run_ok ? 'ac__dry--ok' : 'ac__dry--bad'">
        <div class="ac__drystate">
          <Icon :name="p.dry_run_ok ? 'check' : 'x'" :size="13" />
          {{ p.dry_run_ok ? 'Server dry-run accepted' : "Dry-run failed: can't be approved" }}
        </div>
        <CodeBlock v-if="p.dry_run" :text="p.dry_run" format="auto" :open-up-to="40" label="Dry-run" />
      </section>
    </template>
    <pre v-else class="ac__params mono">{{ params }}</pre>

    <div v-if="action.output || action.error" class="ac__out">
      <button class="ac__toggle" :aria-expanded="showOutput" @click="showOutput = !showOutput">
        <Icon :name="showOutput ? 'chevronDown' : 'chevronRight'" :size="12" /> {{ action.error ? 'Error' : 'Output' }}
      </button>
      <pre v-if="showOutput" class="ac__pre mono" :class="{ 'ac__pre--err': action.error }">{{ action.error || action.output }}</pre>
    </div>

    <footer v-if="pending" class="ac__foot">
      <template v-if="!rejecting">
        <Btn
          variant="primary"
          size="sm"
          icon="check"
          :disabled="!canDecide || dryRunBlocked"
          :loading="busy === 'approve'"
          :title="dryRunBlocked ? 'The server dry-run failed: this change cannot be approved' : undefined"
          @click="emit('approve')"
          >{{ p ? 'Review & approve…' : 'Approve & run' }}</Btn
        >
        <Btn variant="danger" size="sm" icon="x" :disabled="!canDecide" @click="rejecting = true">Reject</Btn>
        <span v-if="!canDecide" class="faint small">operator role required</span>
        <span v-else-if="p" class="faint small">never auto-approved · bound to payload {{ hash8(action.payload_hash) }}</span>
      </template>
      <form v-else class="ac__reject" @submit.prevent="emit('reject', reason || undefined); rejecting = false">
        <input v-model="reason" class="input" placeholder="Reason (optional)" aria-label="Rejection reason" />
        <Btn variant="danger" size="sm" type="submit" :loading="busy === 'reject'">Reject</Btn>
        <Btn variant="ghost" size="sm" @click="rejecting = false">Cancel</Btn>
      </form>
    </footer>
    <footer v-else-if="canRollback" class="ac__foot">
      <Btn size="sm" icon="refresh" :disabled="!canDecide" :loading="rollbackBusy" @click="emit('rollback')">Propose rollback</Btn>
      <span class="faint small mono">{{ p!.rollback_action!.type }}</span>
    </footer>
  </article>
</template>

<style scoped>
.ac {
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  background: var(--bg-panel);
  padding: 12px 14px;
  transition: box-shadow 300ms var(--ease);
}
.ac--pending {
  border-color: color-mix(in srgb, var(--sev-warning) 40%, var(--border));
}
.ac--core {
  border-color: color-mix(in srgb, var(--danger) 45%, var(--border));
}
.ac--superseded {
  opacity: 0.7;
}
.ac--flash {
  box-shadow: 0 0 0 2px var(--accent);
}
.ac__head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.ac__ico {
  color: var(--sev-warning);
}
.ac__summary {
  font-family: var(--font-mono);
  font-weight: 500;
  color: var(--text-strong);
  flex: 1;
  min-width: 180px;
  overflow-wrap: anywhere;
}
.ac__op {
  color: var(--sev-warning);
}
.ac__core {
  font-size: var(--fs-xs);
  font-weight: 600;
  color: var(--text-inverse);
  background: var(--danger);
  border-radius: 4px;
  padding: 1px 6px;
  white-space: nowrap;
}
.ac__meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px 14px;
  margin-top: 6px;
  font-size: var(--fs-xs);
  color: var(--text-dim);
}
.ac__exp--soon {
  color: var(--sev-critical);
}
.ac__hash {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  border: 1px solid var(--border-strong);
  border-radius: 4px;
  background: var(--bg-elev);
  color: var(--text);
  font: inherit;
  padding: 0 6px;
  cursor: copy;
}
.ac__hash:hover {
  border-color: var(--accent);
  color: var(--accent);
}
.ac__line {
  --c: var(--sev-warning);
  display: flex;
  gap: 8px;
  align-items: flex-start;
  margin-top: 10px;
  padding: 7px 10px;
  border-radius: var(--radius);
  font-size: var(--fs-sm);
  background: color-mix(in srgb, var(--c) 10%, transparent);
  border: 1px solid color-mix(in srgb, var(--c) 28%, transparent);
}
.ac__line > :first-child {
  color: var(--c);
  flex: none;
  margin-top: 2px;
}
.ac__line b {
  color: var(--text-strong);
}
.ac__line--info {
  --c: var(--sev-info);
}
.ac__link {
  border: 0;
  background: none;
  padding: 0;
  color: var(--accent);
  font: inherit;
  cursor: pointer;
}
.ac__link:hover {
  text-decoration: underline;
}
.ac__warns {
  list-style: none;
  margin: 8px 0 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 3px;
  font-size: var(--fs-sm);
  color: var(--sev-warning);
}
.ac__warns li {
  display: flex;
  gap: 6px;
  align-items: baseline;
}
.ac__why {
  margin-top: 10px;
  font-size: var(--fs-md);
}
.ac__dl {
  display: grid;
  grid-template-columns: 96px minmax(0, 1fr);
  gap: 4px 10px;
  margin: 10px 0 0;
  font-size: var(--fs-sm);
}
.ac__dl dt {
  color: var(--text-faint);
}
.ac__dl dd {
  margin: 0;
  overflow-wrap: anywhere;
  color: var(--text);
}
.ac__code {
  margin-top: 10px;
}
.ac__dry {
  margin-top: 10px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.ac__drystate {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: var(--fs-sm);
  font-weight: 600;
}
.ac__dry--ok .ac__drystate {
  color: var(--success);
}
.ac__dry--bad .ac__drystate {
  color: var(--danger);
}
.ac__params {
  margin: 10px 0 0;
  padding: 8px 10px;
  background: var(--code-bg);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  font-size: var(--fs-xs);
  max-height: 180px;
  overflow: auto;
}
.ac__toggle {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  border: 0;
  background: none;
  padding: 8px 0 0;
  color: var(--text-dim);
  font-size: var(--fs-sm);
}
.ac__pre {
  margin: 6px 0 0;
  padding: 8px 10px;
  background: var(--code-bg);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  font-size: var(--fs-xs);
  max-height: 240px;
  overflow: auto;
  white-space: pre-wrap;
}
.ac__pre--err {
  color: var(--danger);
}
.ac__foot {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 12px;
  flex-wrap: wrap;
}
.ac__reject {
  display: flex;
  gap: 6px;
  flex: 1;
  min-width: 0;
}
.ac__reject .input {
  min-height: 24px;
  height: 24px;
  font-size: var(--fs-sm);
}
.small {
  font-size: var(--fs-xs);
}
</style>
