import { ref, type Ref } from 'vue'
import type { Action } from '@/api/types'
import { useIncidentMutations } from './useIncident'
import { requestApproval } from '@/lib/approval'

/** Approve/reject with per-action busy state, shared by the stream and the sidebar. */
export function useActionDecisions(incidentId: Ref<number>) {
  const m = useIncidentMutations(incidentId)
  const busyId = ref<number | null>(null)
  const busyKind = ref<'approve' | 'reject' | null>(null)
  /** Prepared (precise) actions go through the confirmation dialog bound to the payload hash. */
  const approve = (a: Action) => {
    if (a.prepared && a.payload_hash) {
      requestApproval(a)
      return
    }
    busyId.value = a.id
    busyKind.value = 'approve'
    m.approve.mutate(a.id, { onSettled: () => (busyId.value = null) })
  }
  const reject = (a: Action, reason?: string) => {
    busyId.value = a.id
    busyKind.value = 'reject'
    m.reject.mutate({ id: a.id, reason }, { onSettled: () => (busyId.value = null) })
  }
  const proposeRollback = (a: Action) => {
    const rb = a.prepared?.rollback_action
    if (!rb) return
    m.propose.mutate({ type: rb.type, params: rb.params, rationale: `rollback of #${a.id}` })
  }
  const busyFor = (a: Action) => (busyId.value === a.id ? busyKind.value : null)
  return { approve, reject, proposeRollback, busyFor, proposing: m.propose.isPending }
}
