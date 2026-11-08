<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useQueryClient } from '@tanstack/vue-query'
import Modal from '../ui/Modal.vue'
import Btn from '../ui/Btn.vue'
import Icon from '../ui/Icon.vue'
import CodeBlock from '../ui/CodeBlock.vue'
import { api, ApiError, errorMessage } from '@/api/client'
import { qk } from '@/api/keys'
import { invalidateIncident } from '@/api/sse'
import { approvalTarget, hash8, revealAction } from '@/lib/approval'
import { toastOk } from '@/lib/toast'

const PREVIEW_LINES = 14

const qc = useQueryClient()
const a = computed(() => approvalTarget.value)
const p = computed(() => a.value?.prepared ?? null)
const open = computed({
  get: () => !!a.value,
  set: (v) => {
    if (!v) approvalTarget.value = null
  },
})

const busy = ref(false)
const error = ref('')
const conflict = ref(false)
const supersededBy = ref<number | null>(null)
watch(a, () => {
  busy.value = false
  error.value = ''
  conflict.value = false
  supersededBy.value = null
})

const lines = computed(() => (p.value?.payload ?? '').split('\n'))
const preview = computed(() => lines.value.slice(0, PREVIEW_LINES).join('\n'))

async function confirm() {
  const action = a.value
  if (!action?.payload_hash || !p.value?.dry_run_ok) return
  busy.value = true
  error.value = ''
  try {
    await api.approve(action.id, action.payload_hash)
    toastOk(`Approved ${hash8(action.payload_hash)}`, p.value ? `${p.value.operation} ${p.value.target}` : undefined)
    invalidateIncident(qc, action.incident_id)
    qc.invalidateQueries({ queryKey: qk.incidents() })
    approvalTarget.value = null
  } catch (e) {
    error.value = errorMessage(e)
    if (e instanceof ApiError && e.status === 409) {
      // The proposal changed under us: show the server message and the superseding proposal.
      conflict.value = true
      const m = /#(\d+)/.exec(error.value)
      if (m) supersededBy.value = Number(m[1])
      invalidateIncident(qc, action.incident_id)
      try {
        const fresh = await api.actions(action.incident_id)
        const self = fresh.items.find((x) => x.id === action.id)
        if (self?.superseded_by) supersededBy.value = self.superseded_by
      } catch {
        /* the stream refreshes anyway */
      }
    }
  } finally {
    busy.value = false
  }
}

function review(id: number) {
  approvalTarget.value = null
  revealAction(id)
}
</script>

<template>
  <Modal v-model:open="open" title="Approve exactly this change?" :description="a ? `Action #${a.id} · ${a.type}. The approval is bound to this payload hash.` : ''" :width="620">
    <div v-if="a && p" class="ad">
      <div v-if="error" class="ad__err" role="alert">
        <Icon name="info" :size="13" />
        <div>
          <b>{{ conflict ? 'The proposal changed.' : 'Approval failed.' }}</b> {{ error }}
          <div v-if="supersededBy" class="ad__review">
            <Btn size="sm" variant="primary" icon="chevronRight" @click="review(supersededBy)">Review the updated proposal #{{ supersededBy }}</Btn>
          </div>
        </div>
      </div>
      <dl class="ad__dl">
        <dt>Target</dt>
        <dd class="mono">{{ p.target }}</dd>
        <dt>Operation</dt>
        <dd class="mono">{{ p.operation }}</dd>
        <dt>Payload hash</dt>
        <dd class="mono ad__hash" :title="a.payload_hash">{{ hash8(a.payload_hash) }}<span class="faint">{{ (a.payload_hash ?? '').slice(8, 24) }}…</span></dd>
        <template v-if="p.effect"><dt>Effect</dt><dd>{{ p.effect }}</dd></template>
      </dl>
      <div v-if="p.core_infrastructure" class="ad__note ad__note--core"><Icon name="admin" :size="13" /> Core infrastructure: double-check the target.</div>
      <div v-if="p.argo_managed" class="ad__note"><Icon name="info" :size="13" /> Managed by Argo CD: this live change will be reverted by the next sync.</div>
      <CodeBlock :text="preview" :format="p.payload_format" :open-up-to="PREVIEW_LINES" :label="lines.length > PREVIEW_LINES ? `Payload preview (${PREVIEW_LINES} of ${lines.length} lines, full payload in the card)` : 'Payload'" />
      <p v-if="!p.dry_run_ok" class="ad__err"><Icon name="x" :size="13" /> The server dry-run failed: this change can't be approved.</p>
    </div>
    <template #footer>
      <Btn variant="ghost" @click="open = false">Cancel</Btn>
      <Btn v-if="a" variant="primary" icon="check" :loading="busy" :disabled="!p?.dry_run_ok || conflict" @click="confirm">
        Approve <span class="mono">{{ hash8(a.payload_hash) }}</span>
      </Btn>
    </template>
  </Modal>
</template>

<style scoped>
.ad {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.ad__dl {
  display: grid;
  grid-template-columns: 104px minmax(0, 1fr);
  gap: 5px 10px;
  margin: 0;
  font-size: var(--fs-sm);
}
.ad__dl dt {
  color: var(--text-faint);
}
.ad__dl dd {
  margin: 0;
  overflow-wrap: anywhere;
  color: var(--text-strong);
}
.ad__hash {
  letter-spacing: 0.02em;
}
.ad__note {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: var(--fs-sm);
  color: var(--sev-warning);
}
.ad__note--core {
  color: var(--danger);
  font-weight: 600;
}
.ad__err {
  display: flex;
  gap: 8px;
  align-items: flex-start;
  margin: 0;
  padding: 8px 10px;
  border-radius: var(--radius);
  background: var(--danger-soft);
  border: 1px solid color-mix(in srgb, var(--danger) 30%, transparent);
  color: var(--text);
  font-size: var(--fs-sm);
}
.ad__err > :first-child {
  color: var(--danger);
  flex: none;
  margin-top: 2px;
}
.ad__err b {
  color: var(--text-strong);
}
.ad__review {
  margin-top: 8px;
}
</style>
