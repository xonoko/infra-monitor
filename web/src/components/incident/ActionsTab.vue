<script setup lang="ts">
import { computed, ref } from 'vue'
import type { Action, IncidentDetail } from '@/api/types'
import ActionCard from './ActionCard.vue'
import ProposeActionForm from './ProposeActionForm.vue'
import Btn from '../ui/Btn.vue'
import Modal from '../ui/Modal.vue'
import Skeleton from '../ui/Skeleton.vue'
import { useActionTypes, useIncidentMutations } from '@/composables/useIncident'
import { useMe } from '@/composables/useMe'

const props = defineProps<{ incident: IncidentDetail; actions?: Action[]; loading?: boolean }>()
const id = computed(() => props.incident.id)
const m = useIncidentMutations(id)
const { isOperator } = useMe()
const types = useActionTypes()

const pending = computed(() => (props.actions ?? []).filter((a) => a.status === 'proposed'))
const history = computed(() => (props.actions ?? []).filter((a) => a.status !== 'proposed'))
const busyId = ref<number | null>(null)
const busyKind = ref<'approve' | 'reject' | null>(null)
const proposing = ref(false)

function approve(a: Action) {
  busyId.value = a.id
  busyKind.value = 'approve'
  m.approve.mutate(a.id, { onSettled: () => (busyId.value = null) })
}
function reject(a: Action, reason?: string) {
  busyId.value = a.id
  busyKind.value = 'reject'
  m.reject.mutate({ id: a.id, reason }, { onSettled: () => (busyId.value = null) })
}
function propose(v: { type: string; params: Record<string, unknown>; rationale?: string }) {
  m.propose.mutate(v, { onSuccess: () => (proposing.value = false) })
}
const defaults = computed(() => ({ namespace: props.incident.namespace, cluster: props.incident.cluster }))
</script>

<template>
  <div class="at">
    <div class="at__bar">
      <span class="section-title">Pending approval</span>
      <span class="spacer" />
      <Btn size="sm" icon="plus" :disabled="!isOperator || !types.data.value?.length" @click="proposing = true">Propose action</Btn>
    </div>
    <Skeleton v-if="loading" :lines="4" />
    <template v-else>
      <div v-if="pending.length" class="at__list">
        <ActionCard
          v-for="a in pending"
          :key="a.id"
          :action="a"
          :can-decide="isOperator"
          :busy="busyId === a.id ? busyKind : null"
          @approve="approve(a)"
          @reject="(r?: string) => reject(a, r)"
        />
      </div>
      <p v-else class="muted at__none">Nothing waiting. The agent proposes actions after an investigation; operators can also propose one.</p>

      <template v-if="history.length">
        <div class="at__bar at__bar--hist"><span class="section-title">History</span></div>
        <div class="at__list">
          <ActionCard v-for="a in history" :key="a.id" :action="a" :can-decide="false" />
        </div>
      </template>
    </template>

    <Modal v-model:open="proposing" title="Propose an action" description="It still needs approval by an operator before it runs." :width="520">
      <ProposeActionForm v-if="types.data.value?.length" :types="types.data.value" :defaults="defaults" :busy="m.propose.isPending.value" @submit="propose" @cancel="proposing = false" />
    </Modal>
  </div>
</template>

<style scoped>
.at__bar {
  display: flex;
  align-items: center;
  margin-bottom: 10px;
}
.at__bar--hist {
  margin-top: 22px;
}
.spacer {
  flex: 1;
}
.at__list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.at__none {
  margin: 0;
  padding: 14px;
  border: 1px dashed var(--border-strong);
  border-radius: var(--radius-lg);
  font-size: var(--fs-sm);
}
</style>
