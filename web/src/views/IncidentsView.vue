<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { keepPreviousData, useInfiniteQuery, useQuery } from '@tanstack/vue-query'
import SearchBar from '@/components/SearchBar.vue'
import TimeRangePicker from '@/components/TimeRangePicker.vue'
import Histogram from '@/components/Histogram.vue'
import IncidentTable from '@/components/IncidentTable.vue'
import IncidentDrawer from '@/components/IncidentDrawer.vue'
import Btn from '@/components/ui/Btn.vue'
import Icon from '@/components/ui/Icon.vue'
import Kbd from '@/components/ui/Kbd.vue'
import Skeleton from '@/components/ui/Skeleton.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import { api, errorMessage } from '@/api/client'
import { qk } from '@/api/keys'
import { hasTokenValue, parseSearch, resolveRange, toggleToken, type TimeRange } from '@/lib/search'
import { useNow } from '@/lib/now'
import { useHotkeys } from '@/composables/useHotkeys'

const route = useRoute()
const router = useRouter()
const now = useNow()

const q1 = (v: unknown) => (Array.isArray(v) ? v[0] : v) as string | undefined

// ---- URL state ----
const search = computed({
  get: () => q1(route.query.s) ?? '',
  set: (s: string) => void router.replace({ query: { ...route.query, s: s || undefined } }),
})
const range = computed<TimeRange>({
  get: () => {
    const since = q1(route.query.since)
    if (since) return { since, until: q1(route.query.until) }
    return { preset: q1(route.query.range) ?? '24h' }
  },
  set: (r) =>
    void router.replace({
      query: { ...route.query, range: r.preset && r.preset !== '24h' ? r.preset : undefined, since: r.since, until: r.until },
    }),
})
const selectedId = computed<number | null>(() => {
  const v = Number(q1(route.query.incident))
  return Number.isFinite(v) && v > 0 ? v : null
})
function openIncident(id: number | null) {
  void router.replace({ query: { ...route.query, incident: id ? String(id) : undefined } })
}

// ---- data ----
const minute = computed(() => Math.floor(now.value / 30000) * 30000)
const filters = computed(() => ({ ...parseSearch(search.value), ...resolveRange(range.value, minute.value) }))

const list = useInfiniteQuery({
  queryKey: computed(() => qk.incidents(filters.value)),
  queryFn: ({ pageParam }) => api.incidents(filters.value, pageParam),
  initialPageParam: undefined as string | undefined,
  getNextPageParam: (last) => last.next_cursor || undefined,
  placeholderData: keepPreviousData,
})
const rows = computed(() => list.data.value?.pages.flatMap((p) => p.items) ?? [])
const total = computed(() => list.data.value?.pages[0]?.total ?? 0)

const hist = useQuery({
  queryKey: computed(() => qk.histogram(filters.value)),
  queryFn: () => api.histogram(filters.value, 60),
  placeholderData: keepPreviousData,
})

const suggestions = computed(() => ({
  cluster: [...new Set(rows.value.map((r) => r.cluster).filter(Boolean))],
  namespace: [...new Set(rows.value.map((r) => r.namespace).filter(Boolean))],
}))

// ---- quick filters ----
const quick = [
  { key: 'status', value: 'open', label: 'Open' },
  { key: 'status', value: 'acknowledged', label: 'Acknowledged' },
  { key: 'status', value: 'resolved', label: 'Resolved' },
  { key: 'severity', value: 'critical', label: 'Critical' },
  { key: 'verdict', value: 'actionable', label: 'Actionable' },
] as const

// ---- keyboard ----
const cursor = ref(0)
watch(rows, (r) => {
  if (cursor.value >= r.length) cursor.value = Math.max(0, r.length - 1)
})
watch(selectedId, (id) => {
  const i = rows.value.findIndex((r) => r.id === id)
  if (i >= 0) cursor.value = i
})
const searchBar = ref<InstanceType<typeof SearchBar>>()
useHotkeys({
  j: () => {
    cursor.value = Math.min(rows.value.length - 1, cursor.value + 1)
    if (selectedId.value !== null && rows.value[cursor.value]) openIncident(rows.value[cursor.value].id)
  },
  k: () => {
    cursor.value = Math.max(0, cursor.value - 1)
    if (selectedId.value !== null && rows.value[cursor.value]) openIncident(rows.value[cursor.value].id)
  },
  Enter: () => rows.value[cursor.value] && openIncident(rows.value[cursor.value].id),
  Escape: () => selectedId.value !== null && openIncident(null),
  '/': (e) => {
    e.preventDefault()
    searchBar.value?.focus()
  },
})

function onHistSelect(since: string, until: string) {
  range.value = { since, until }
}
</script>

<template>
  <div class="inc" :class="{ 'inc--drawer': selectedId !== null }">
    <div class="topbar">
      <SearchBar ref="searchBar" v-model="search" :suggestions="suggestions" />
      <TimeRangePicker v-model="range" />
      <Btn variant="ghost" icon="refresh" aria-label="Refresh" title="Refresh" :loading="list.isFetching.value && !list.isFetchingNextPage.value" @click="list.refetch(); hist.refetch()" />
    </div>

    <div class="quick">
      <button
        v-for="qf in quick"
        :key="qf.label"
        class="quick__chip"
        :class="[`quick__chip--${qf.value}`, { 'quick__chip--on': hasTokenValue(search, qf.key, qf.value) }]"
        :aria-pressed="hasTokenValue(search, qf.key, qf.value)"
        @click="search = toggleToken(search, qf.key, qf.value)"
      >
        {{ qf.label }}
      </button>
      <button v-if="range.since" class="quick__chip quick__chip--range" @click="range = { preset: '24h' }"><Icon name="x" :size="11" /> custom range</button>
      <span class="quick__count mono">
        <template v-if="list.isSuccess.value">{{ rows.length }}<span class="faint"> / {{ total }}</span> incidents</template>
      </span>
      <span class="quick__keys faint"><Kbd>j</Kbd><Kbd>k</Kbd> move <Kbd>↵</Kbd> open <Kbd>/</Kbd> search</span>
    </div>

    <Histogram :buckets="hist.data.value?.buckets ?? []" :loading="hist.isFetching.value" @select="onHistSelect" />

    <div class="inc__list">
      <div v-if="list.isPending.value" class="inc__pad"><Skeleton :lines="10" :height="20" /></div>
      <EmptyState v-else-if="list.isError.value" icon="info" title="Could not load incidents" :text="errorMessage(list.error.value)">
        <Btn icon="refresh" @click="list.refetch()">Retry</Btn>
      </EmptyState>
      <EmptyState
        v-else-if="!rows.length"
        icon="check"
        title="No incidents match"
        :text="search ? 'Try removing a filter or widening the time range.' : 'All quiet in this time range.'"
      >
        <Btn v-if="search" @click="search = ''">Clear search</Btn>
      </EmptyState>
      <template v-else>
        <IncidentTable :rows="rows" :selected-id="selectedId" :compact="selectedId !== null" :cursor="cursor" @update:cursor="(i: number) => (cursor = i)" @open="openIncident" />
        <div v-if="list.hasNextPage.value" class="inc__more">
          <Btn :loading="list.isFetchingNextPage.value" @click="list.fetchNextPage()">Load more</Btn>
        </div>
      </template>
    </div>

    <IncidentDrawer :id="selectedId" @close="openIncident(null)" @expand="(id: number) => router.push(`/incidents/${id}`)" />
  </div>
</template>

<style scoped>
.inc {
  display: flex;
  flex-direction: column;
  min-height: 100%;
}
.topbar {
  position: sticky;
  top: 0;
  z-index: 5;
  display: flex;
  gap: 8px;
  align-items: center;
  height: var(--topbar-h);
  padding: 0 16px;
  background: var(--bg-elev);
  border-bottom: 1px solid var(--border);
}
.quick {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 16px;
  background: var(--bg-elev);
  border-bottom: 1px solid var(--border);
  flex-wrap: wrap;
}
.quick__chip {
  --c: var(--text-dim);
  height: 24px;
  padding: 0 10px;
  border-radius: 12px;
  border: 1px solid var(--border-strong);
  background: transparent;
  color: var(--text-dim);
  font-size: var(--fs-sm);
  display: inline-flex;
  align-items: center;
  gap: 4px;
}
.quick__chip:hover {
  color: var(--text-strong);
  border-color: var(--text-faint);
}
.quick__chip--open {
  --c: var(--st-open);
}
.quick__chip--acknowledged {
  --c: var(--st-ack);
}
.quick__chip--resolved {
  --c: var(--st-resolved);
}
.quick__chip--critical {
  --c: var(--sev-critical);
}
.quick__chip--actionable {
  --c: var(--sev-warning);
}
.quick__chip--on {
  color: var(--c);
  border-color: color-mix(in srgb, var(--c) 55%, transparent);
  background: color-mix(in srgb, var(--c) 12%, transparent);
}
.quick__chip--range {
  color: var(--accent);
  border-color: var(--accent-ring);
}
.quick__count {
  margin-left: auto;
  font-size: var(--fs-sm);
  color: var(--text);
}
.quick__keys {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: var(--fs-xs);
}
.inc__list {
  flex: 1;
  background: var(--bg);
}
.inc__pad {
  padding: 16px;
}
.inc__more {
  display: flex;
  justify-content: center;
  padding: 14px;
}
@media (min-width: 901px) {
  .inc--drawer .inc__list {
    margin-right: var(--drawer-w);
  }
}
@media (max-width: 900px) {
  .quick__keys {
    display: none;
  }
}
@media (max-width: 640px) {
  .topbar {
    padding: 0 10px;
  }
  .quick {
    padding: 8px 10px;
  }
}
</style>
