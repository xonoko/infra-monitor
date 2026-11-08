import { computed, type Ref } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { api } from '@/api/client'
import { qk } from '@/api/keys'
import { invalidateIncident } from '@/api/sse'
import { toastError, toastOk } from '@/lib/toast'

export function useIncidentData(id: Ref<number>) {
  const enabled = computed(() => id.value > 0)
  const incident = useQuery({ queryKey: computed(() => qk.incident(id.value)), queryFn: () => api.incident(id.value), enabled })
  const timeline = useQuery({
    queryKey: computed(() => qk.timeline(id.value)),
    queryFn: () => api.timeline(id.value).then((r) => r.items),
    enabled,
  })
  const signals = useQuery({ queryKey: computed(() => qk.signals(id.value)), queryFn: () => api.signals(id.value).then((r) => r.items), enabled })
  const investigations = useQuery({
    queryKey: computed(() => qk.investigations(id.value)),
    queryFn: () => api.investigations(id.value).then((r) => r.items),
    enabled,
  })
  const messages = useQuery({ queryKey: computed(() => qk.messages(id.value)), queryFn: () => api.messages(id.value).then((r) => r.items), enabled })
  const actions = useQuery({ queryKey: computed(() => qk.actions(id.value)), queryFn: () => api.actions(id.value).then((r) => r.items), enabled })
  return { incident, timeline, signals, investigations, messages, actions }
}

export function useActionTypes() {
  return useQuery({ queryKey: qk.actionTypes, queryFn: () => api.actionTypes().then((r) => r.items), staleTime: 5 * 60_000 })
}

/** Mutations that act on one incident; they refresh it and the lists. */
export function useIncidentMutations(id: Ref<number>) {
  const qc = useQueryClient()
  const refresh = () => {
    invalidateIncident(qc, id.value)
    qc.invalidateQueries({ queryKey: qk.incidents() })
  }
  const mk = <A, R>(fn: (a: A) => Promise<R>, ok: string | ((a: A) => string) | null, fail: string) =>
    useMutation({
      mutationFn: fn,
      onSuccess: (_r, a) => {
        if (ok) toastOk(typeof ok === 'function' ? ok(a) : ok)
        refresh()
      },
      onError: (e) => toastError(fail, e),
    })
  return {
    ack: mk(() => api.ack(id.value), 'Incident acknowledged', 'Could not acknowledge'),
    resolve: mk((reason?: string) => api.resolve(id.value, reason), 'Incident resolved', 'Could not resolve'),
    reopen: mk(() => api.reopen(id.value), 'Incident reopened', 'Could not reopen'),
    reinvestigate: mk(() => api.reinvestigate(id.value), 'Investigation requested', 'Could not start investigation'),
    mute: mk(
      (a: { duration: string; reason?: string }) => api.muteIncident(id.value, a.duration, a.reason),
      (a) => `Muted for ${a.duration}`,
      'Could not mute',
    ),
    approve: mk((actionId: number) => api.approve(actionId), 'Action approved', 'Could not approve'),
    reject: mk((a: { id: number; reason?: string }) => api.reject(a.id, a.reason), 'Action rejected', 'Could not reject'),
    propose: mk(
      (a: { type: string; params: Record<string, unknown>; rationale?: string }) => api.proposeAction(id.value, a),
      'Action proposed — waiting for approval',
      'Could not propose action',
    ),
    send: mk((content: string) => api.postMessage(id.value, content), null, 'Message not sent'),
  }
}
