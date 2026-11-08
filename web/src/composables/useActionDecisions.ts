import { ref, type Ref } from 'vue'
import type { Action } from '@/api/types'
import { useIncidentMutations } from './useIncident'

/** Approve/reject with per-action busy state, shared by the stream and the sidebar. */
export function useActionDecisions(incidentId: Ref<number>) {
  const m = useIncidentMutations(incidentId)
  const busyId = ref<number | null>(null)
  const busyKind = ref<'approve' | 'reject' | null>(null)
  const approve = (a: Action) => {
    busyId.value = a.id
    busyKind.value = 'approve'
    m.approve.mutate(a.id, { onSettled: () => (busyId.value = null) })
  }
  const reject = (a: Action, reason?: string) => {
    busyId.value = a.id
    busyKind.value = 'reject'
    m.reject.mutate({ id: a.id, reason }, { onSettled: () => (busyId.value = null) })
  }
  const busyFor = (a: Action) => (busyId.value === a.id ? busyKind.value : null)
  return { approve, reject, busyFor }
}
