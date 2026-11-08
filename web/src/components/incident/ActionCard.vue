<script setup lang="ts">
import { computed, ref } from 'vue'
import type { Action } from '@/api/types'
import Chip from '../ui/Chip.vue'
import Btn from '../ui/Btn.vue'
import Icon from '../ui/Icon.vue'
import Markdown from '../ui/Markdown.vue'
import { absTime, countdown, relTime } from '@/lib/format'
import { useNow } from '@/lib/now'

const props = defineProps<{ action: Action; canDecide: boolean; busy?: 'approve' | 'reject' | null }>()
const emit = defineEmits<{ approve: []; reject: [reason?: string] }>()
const now = useNow()

const pending = computed(() => props.action.status === 'proposed')
const expiresSoon = computed(() => new Date(props.action.expires_at).getTime() - now.value < 5 * 60e3)
const rejecting = ref(false)
const reason = ref('')
const showOutput = ref(false)
const params = computed(() => JSON.stringify(props.action.params ?? {}, null, 2))
</script>

<template>
  <article class="ac" :class="{ 'ac--pending': pending }">
    <header class="ac__head">
      <Icon name="bolt" :size="14" class="ac__ico" />
      <span class="ac__summary">{{ action.summary || action.type }}</span>
      <Chip kind="risk" :value="`${action.risk} risk`" dense />
      <Chip kind="action" :value="action.status" dense />
    </header>
    <div class="ac__meta mono">
      <span>{{ action.type }}</span>
      <span>proposed by {{ action.proposed_by }} · {{ relTime(action.created_at, now) }}</span>
      <span v-if="pending" :class="{ 'ac__exp--soon': expiresSoon }" :title="`expires ${absTime(action.expires_at)}`"><Icon name="clock" :size="11" /> {{ countdown(action.expires_at, now) }}</span>
      <span v-else-if="action.decided_by_name">
        {{ action.status === 'rejected' ? 'rejected' : 'approved' }} by {{ action.decided_by_name }}<template v-if="action.decided_via"> via {{ action.decided_via }}</template>
      </span>
    </div>
    <div v-if="action.rationale" class="ac__why"><Markdown :source="action.rationale" /></div>
    <pre class="ac__params mono">{{ params }}</pre>
    <div v-if="action.output || action.error" class="ac__out">
      <button class="ac__toggle" :aria-expanded="showOutput" @click="showOutput = !showOutput">
        <Icon :name="showOutput ? 'chevronDown' : 'chevronRight'" :size="12" /> {{ action.error ? 'Error' : 'Output' }}
      </button>
      <pre v-if="showOutput" class="ac__pre mono" :class="{ 'ac__pre--err': action.error }">{{ action.error || action.output }}</pre>
    </div>
    <footer v-if="pending" class="ac__foot">
      <template v-if="!rejecting">
        <Btn variant="primary" size="sm" icon="check" :disabled="!canDecide" :loading="busy === 'approve'" @click="emit('approve')">Approve & run</Btn>
        <Btn variant="danger" size="sm" icon="x" :disabled="!canDecide" @click="rejecting = true">Reject</Btn>
        <span v-if="!canDecide" class="faint small">operator role required</span>
      </template>
      <form v-else class="ac__reject" @submit.prevent="emit('reject', reason || undefined); rejecting = false">
        <input v-model="reason" class="input" placeholder="Reason (optional)" aria-label="Rejection reason" />
        <Btn variant="danger" size="sm" type="submit" :loading="busy === 'reject'">Reject</Btn>
        <Btn variant="ghost" size="sm" @click="rejecting = false">Cancel</Btn>
      </form>
    </footer>
  </article>
</template>

<style scoped>
.ac {
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  background: var(--bg-panel);
  padding: 12px 14px;
}
.ac--pending {
  border-color: color-mix(in srgb, var(--sev-warning) 40%, var(--border));
  box-shadow: inset 3px 0 0 var(--sev-warning);
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
.ac__meta {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 14px;
  margin-top: 6px;
  font-size: var(--fs-xs);
  color: var(--text-dim);
}
.ac__exp--soon {
  color: var(--sev-critical);
}
.ac__why {
  margin-top: 10px;
  font-size: var(--fs-md);
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
