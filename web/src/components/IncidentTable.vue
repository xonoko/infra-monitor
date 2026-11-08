<script setup lang="ts">
import { computed, h, nextTick, ref, watch } from 'vue'
import { FlexRender, getCoreRowModel, useVueTable, type ColumnDef } from '@tanstack/vue-table'
import type { Incident } from '@/api/types'
import Chip from './ui/Chip.vue'
import Icon from './ui/Icon.vue'
import { age, absTime, sevKey } from '@/lib/format'
import { useNow } from '@/lib/now'
import { useMedia } from '@/composables/useMedia'

const props = defineProps<{ rows: Incident[]; selectedId?: number | null; cursor: number; compact?: boolean }>()
const emit = defineEmits<{ open: [id: number]; 'update:cursor': [i: number] }>()

const now = useNow()

const columns: ColumnDef<Incident>[] = [
  {
    id: 'sev',
    header: '',
    size: 6,
    cell: ({ row }) => h('span', { class: `stripe stripe--${sevKey(row.original.severity)}`, title: row.original.severity || 'no severity' }),
  },
  {
    id: 'id',
    header: '#',
    size: 56,
    cell: ({ row }) => h('span', { class: 'mono faint' }, String(row.original.id)),
  },
  {
    id: 'title',
    header: 'Incident',
    cell: ({ row }) => {
      const i = row.original
      return h('div', { class: 'tcell-title' }, [
        h('div', { class: 'tcell-title__main' }, [
          i.muted ? h(Icon, { name: 'mute', size: 13, class: 'muted-ico', title: 'muted' }) : null,
          i.internal ? h('span', { class: 'internal-tag mono' }, 'internal') : null,
          h('span', { class: 'tcell-title__text' }, i.title),
        ]),
        i.summary ? h('div', { class: 'tcell-title__sum' }, i.summary) : null,
      ])
    },
  },
  {
    id: 'status',
    header: 'Status',
    size: 104,
    cell: ({ row }) => h(Chip, { kind: 'status', value: row.original.status, dense: true }),
  },
  {
    id: 'verdict',
    header: 'Verdict',
    size: 120,
    cell: ({ row }) => {
      const i = row.original
      if (i.investigation_state === 'running' || i.investigation_state === 'pending')
        return h('span', { class: 'investigating mono' }, [h('span', { class: 'pulse' }), 'investigating'])
      return h(Chip, { kind: 'verdict', value: i.verdict, dense: true })
    },
  },
  {
    id: 'where',
    header: 'Cluster / namespace',
    size: 190,
    cell: ({ row }) =>
      h('span', { class: 'mono where' }, [
        h('span', { class: 'where__c' }, row.original.cluster || '—'),
        row.original.namespace ? h('span', { class: 'faint' }, ' / ') : null,
        row.original.namespace ? h('span', null, row.original.namespace) : null,
      ]),
  },
  {
    id: 'signals',
    header: 'Firing',
    size: 64,
    cell: ({ row }) => {
      const i = row.original
      return h('span', { class: ['mono', 'num', i.firing_count ? 'num--hot' : 'faint'], title: `${i.firing_count} firing of ${i.signal_count} signals` }, `${i.firing_count}/${i.signal_count}`)
    },
  },
  {
    id: 'actions',
    header: 'Actions',
    size: 72,
    cell: ({ row }) =>
      row.original.pending_actions
        ? h('span', { class: 'pending mono', title: `${row.original.pending_actions} pending approval` }, [h(Icon, { name: 'bolt', size: 12 }), String(row.original.pending_actions)])
        : h('span', { class: 'faint' }, '—'),
  },
  {
    id: 'age',
    header: 'Last',
    size: 64,
    cell: ({ row }) => {
      void now.value
      return h('span', { class: 'mono dim', title: `opened ${absTime(row.original.opened_at)}\nlast signal ${absTime(row.original.last_signal_at)}` }, age(row.original.last_signal_at, now.value))
    },
  },
]

const narrow = useMedia('(max-width: 900px)')
const phone = useMedia('(max-width: 640px)')
const visibility = computed<Record<string, boolean>>(() => ({
  id: !narrow.value,
  verdict: !narrow.value && !props.compact,
  actions: !narrow.value && !props.compact,
  where: !phone.value && !props.compact,
  signals: !phone.value && !props.compact,
}))

const table = useVueTable({
  get data() {
    return props.rows
  },
  state: {
    get columnVisibility() {
      return visibility.value
    },
  },
  columns,
  getCoreRowModel: getCoreRowModel(),
  getRowId: (r) => String(r.id),
})

const body = ref<HTMLElement>()
watch(
  () => props.cursor,
  async (c) => {
    await nextTick()
    const el = body.value?.querySelector<HTMLElement>(`[data-row="${c}"]`)
    el?.scrollIntoView({ block: 'nearest' })
  },
)

const colWidths = computed(() => {
  void visibility.value
  return table.getVisibleLeafColumns().map((c) => (c.id !== 'title' && c.columnDef.size ? `${c.columnDef.size}px` : 'auto'))
})
</script>

<template>
  <div class="itable" role="grid" aria-label="Incidents">
    <table class="table">
      <colgroup>
        <col v-for="(w, i) in colWidths" :key="i" :style="{ width: w }" />
      </colgroup>
      <thead>
        <tr v-for="hg in table.getHeaderGroups()" :key="hg.id">
          <th v-for="header in hg.headers.filter((x) => x.column.getIsVisible())" :key="header.id" :class="`col-${header.column.id}`">
            <FlexRender :render="header.column.columnDef.header" :props="header.getContext()" />
          </th>
        </tr>
      </thead>
      <tbody ref="body">
        <tr
          v-for="(row, idx) in table.getRowModel().rows"
          :key="row.id"
          :data-row="idx"
          class="irow"
          :class="{ 'irow--sel': row.original.id === selectedId, 'irow--cursor': idx === cursor, 'irow--resolved': row.original.status === 'resolved' || row.original.status === 'closed' }"
          :aria-selected="row.original.id === selectedId"
          tabindex="-1"
          @click="emit('update:cursor', idx); emit('open', row.original.id)"
        >
          <td v-for="cell in row.getVisibleCells()" :key="cell.id" :class="`col-${cell.column.id}`">
            <FlexRender :render="cell.column.columnDef.cell" :props="cell.getContext()" />
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<style scoped>
.itable {
  min-width: 0;
  overflow-x: hidden;
}
.table {
  table-layout: fixed;
}
.table td {
  height: var(--row-h);
  padding-top: 5px;
  padding-bottom: 5px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.table th.col-sev,
.table td.col-sev {
  padding: 0;
  position: relative;
}
.irow {
  cursor: pointer;
}
.irow--cursor td {
  background: var(--bg-hover);
}
.irow--sel td {
  background: var(--bg-active) !important;
}
.irow--sel td.col-sev::after {
  content: '';
  position: absolute;
  inset: 0 auto 0 0;
  width: 2px;
  background: var(--accent);
}
.irow--resolved :deep(.tcell-title__text) {
  color: var(--text-dim);
}
:deep(.stripe) {
  position: absolute;
  left: 0;
  top: 3px;
  bottom: 3px;
  width: 3px;
  border-radius: 0 2px 2px 0;
  background: var(--sev-other);
}
:deep(.stripe--critical) {
  background: var(--sev-critical);
}
:deep(.stripe--warning) {
  background: var(--sev-warning);
}
:deep(.stripe--info) {
  background: var(--sev-info);
}
:deep(.tcell-title) {
  min-width: 0;
  overflow: hidden;
}
:deep(.tcell-title__main) {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
:deep(.tcell-title__text) {
  color: var(--text-strong);
  font-weight: 500;
  overflow: hidden;
  text-overflow: ellipsis;
}
:deep(.tcell-title__sum) {
  font-size: var(--fs-xs);
  color: var(--text-dim);
  overflow: hidden;
  text-overflow: ellipsis;
  margin-top: 1px;
}
:deep(.muted-ico) {
  color: var(--text-faint);
}
:deep(.internal-tag) {
  font-size: 10px;
  padding: 0 4px;
  border-radius: 3px;
  border: 1px solid var(--border-strong);
  color: var(--text-dim);
}
:deep(.where) {
  font-size: var(--fs-sm);
  color: var(--text);
  overflow: hidden;
  text-overflow: ellipsis;
}
:deep(.where__c) {
  color: var(--text-dim);
}
:deep(.num) {
  font-size: var(--fs-sm);
}
:deep(.num--hot) {
  color: var(--sev-critical);
}
:deep(.dim) {
  color: var(--text-dim);
  font-size: var(--fs-sm);
}
:deep(.pending) {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  color: var(--sev-warning);
  font-size: var(--fs-sm);
  font-weight: 500;
}
:deep(.investigating) {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: var(--fs-xs);
  color: var(--accent);
}
:deep(.pulse) {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--accent);
  animation: pulse 1.2s ease-in-out infinite;
}
@keyframes pulse {
  50% {
    opacity: 0.25;
    transform: scale(0.7);
  }
}
</style>
