import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { api, setCsrfToken } from '@/api/client'
import { qk } from '@/api/keys'
import type { Me, Role } from '@/api/types'

const level: Record<Role, number> = { viewer: 1, operator: 2, admin: 3 }

export async function fetchMe(): Promise<Me> {
  const me = await api.me()
  setCsrfToken(me.csrf_token)
  return me
}

export function useMe() {
  const q = useQuery({ queryKey: qk.me, queryFn: fetchMe, staleTime: 60_000, retry: false })
  const me = computed(() => q.data.value)
  const can = (role: Role) => !!me.value && level[me.value.role] >= level[role]
  const isOperator = computed(() => can('operator'))
  const isAdmin = computed(() => can('admin'))
  const traceURL = (traceId?: string) => {
    const tpl = me.value?.trace_url_template
    if (!tpl || !traceId) return ''
    return tpl.replace('{trace_id}', traceId)
  }
  return { ...q, me, can, isOperator, isAdmin, traceURL }
}
