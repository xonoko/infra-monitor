<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import type { HistogramBucket } from '@/api/types'

const props = defineProps<{ buckets: HistogramBucket[]; loading?: boolean }>()
const emit = defineEmits<{ select: [since: string, until: string] }>()

const SERIES = [
  { key: 'critical', color: 'var(--sev-critical)', label: 'critical' },
  { key: 'warning', color: 'var(--sev-warning)', label: 'warning' },
  { key: 'info', color: 'var(--sev-info)', label: 'info' },
  { key: 'other', color: 'var(--sev-other)', label: 'other' },
] as const

const H = 84
const AXIS = 16
const el = ref<HTMLDivElement>()
const width = ref(800)
let ro: ResizeObserver | undefined
onMounted(() => {
  ro = new ResizeObserver(([e]) => (width.value = Math.max(200, e.contentRect.width)))
  if (el.value) ro.observe(el.value)
})
onBeforeUnmount(() => ro?.disconnect())

const total = (b: HistogramBucket) => b.critical + b.warning + b.info + b.other
const max = computed(() => Math.max(1, ...props.buckets.map(total)))
const bw = computed(() => (props.buckets.length ? width.value / props.buckets.length : width.value))
const sum = computed(() => props.buckets.reduce((a, b) => a + total(b), 0))

const bars = computed(() =>
  props.buckets.map((b, i) => {
    let y = H
    const segs = SERIES.map((s) => {
      const v = b[s.key]
      const h = (v / max.value) * (H - 6)
      y -= h
      return { key: s.key, color: s.color, y, h, v }
    }).filter((s) => s.v > 0)
    return { i, x: i * bw.value, b, segs, total: total(b) }
  }),
)

const ticks = computed(() => {
  const n = props.buckets.length
  if (!n) return []
  const span = new Date(props.buckets[n - 1].end).getTime() - new Date(props.buckets[0].start).getTime()
  const count = Math.min(6, Math.max(2, Math.floor(width.value / 140)))
  const step = Math.max(1, Math.floor(n / count))
  const fmt = (d: Date) =>
    span > 2 * 86400e3
      ? d.toLocaleDateString(undefined, { month: 'short', day: '2-digit' })
      : d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
  const out = []
  for (let i = 0; i < n; i += step) out.push({ x: i * bw.value, label: fmt(new Date(props.buckets[i].start)) })
  return out
})

// hover + drag selection
const hover = ref<number | null>(null)
const dragFrom = ref<number | null>(null)
const dragTo = ref<number | null>(null)

function idxAt(e: PointerEvent) {
  const r = el.value!.getBoundingClientRect()
  const x = Math.min(Math.max(0, e.clientX - r.left), r.width - 1)
  return Math.min(props.buckets.length - 1, Math.floor(x / bw.value))
}
function down(e: PointerEvent) {
  if (!props.buckets.length) return
  ;(e.currentTarget as Element).setPointerCapture(e.pointerId)
  dragFrom.value = dragTo.value = idxAt(e)
}
function move(e: PointerEvent) {
  if (!props.buckets.length) return
  const i = idxAt(e)
  hover.value = i
  if (dragFrom.value !== null) dragTo.value = i
}
function up() {
  if (dragFrom.value === null || dragTo.value === null) return
  const a = Math.min(dragFrom.value, dragTo.value)
  const b = Math.max(dragFrom.value, dragTo.value)
  dragFrom.value = dragTo.value = null
  if (b - a < 1) return // a click, not a drag
  emit('select', props.buckets[a].start, props.buckets[b].end)
}
const sel = computed(() => {
  if (dragFrom.value === null || dragTo.value === null) return null
  const a = Math.min(dragFrom.value, dragTo.value)
  const b = Math.max(dragFrom.value, dragTo.value)
  return { x: a * bw.value, w: (b - a + 1) * bw.value }
})
const hovered = computed(() => (hover.value !== null ? props.buckets[hover.value] : null))
const fmtRange = (b: HistogramBucket) =>
  `${new Date(b.start).toLocaleString(undefined, { month: 'short', day: '2-digit', hour: '2-digit', minute: '2-digit' })} – ${new Date(
    b.end,
  ).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })}`
</script>

<template>
  <div class="hist">
    <div class="hist__head">
      <span class="section-title">Incidents over time</span>
      <span class="mono faint">{{ sum }} total</span>
      <span class="hist__legend">
        <span v-for="s in SERIES" :key="s.key" class="hist__lg"><i :style="{ background: s.color }" />{{ s.label }}</span>
      </span>
      <span class="hist__tip mono" :class="{ 'hist__tip--on': hovered }">
        <template v-if="hovered">
          {{ fmtRange(hovered) }} ·
          <b>{{ total(hovered) }}</b>
          <span v-if="hovered.critical" class="c">{{ hovered.critical }} crit</span>
          <span v-if="hovered.warning" class="w">{{ hovered.warning }} warn</span>
        </template>
        <template v-else>drag to zoom</template>
      </span>
    </div>
    <div
      ref="el"
      class="hist__plot"
      :class="{ 'hist__plot--loading': loading }"
      @pointerdown="down"
      @pointermove="move"
      @pointerup="up"
      @pointerleave="hover = null"
    >
      <svg :width="width" :height="H + AXIS" role="img" :aria-label="`Histogram: ${sum} incidents`">
        <line v-for="g in 3" :key="g" x1="0" :x2="width" :y1="(H / 4) * g" :y2="(H / 4) * g" class="grid" />
        <line x1="0" :x2="width" :y1="H + 0.5" :y2="H + 0.5" class="axis" />
        <g v-for="bar in bars" :key="bar.i">
          <rect v-if="hover === bar.i" :x="bar.x" y="0" :width="bw" :height="H" class="hoverband" />
          <rect
            v-for="s in bar.segs"
            :key="s.key"
            :x="bar.x + Math.min(1, bw * 0.12)"
            :y="s.y"
            :width="Math.max(1, bw - Math.min(2, bw * 0.24))"
            :height="Math.max(1, s.h)"
            :fill="s.color"
            rx="1"
          />
        </g>
        <rect v-if="sel" :x="sel.x" y="0" :width="sel.w" :height="H" class="selband" />
        <text v-for="t in ticks" :key="t.x" :x="t.x + 2" :y="H + 12" class="tick">{{ t.label }}</text>
      </svg>
    </div>
  </div>
</template>

<style scoped>
.hist {
  padding: 10px 16px 6px;
  border-bottom: 1px solid var(--border);
  background: var(--bg-elev);
}
.hist__head {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 6px;
  font-size: var(--fs-sm);
  flex-wrap: wrap;
}
.hist__legend {
  display: flex;
  gap: 10px;
  color: var(--text-dim);
  font-size: var(--fs-xs);
}
.hist__lg {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}
.hist__lg i {
  width: 8px;
  height: 8px;
  border-radius: 2px;
}
.hist__tip {
  margin-left: auto;
  font-size: var(--fs-xs);
  color: var(--text-faint);
  display: flex;
  gap: 8px;
}
.hist__tip--on {
  color: var(--text);
}
.hist__tip b {
  color: var(--text-strong);
}
.c {
  color: var(--sev-critical);
}
.w {
  color: var(--sev-warning);
}
.hist__plot {
  cursor: crosshair;
  user-select: none;
  touch-action: none;
}
.hist__plot--loading {
  opacity: 0.55;
}
svg {
  display: block;
}
.grid {
  stroke: var(--border);
  stroke-dasharray: 2 4;
}
.axis {
  stroke: var(--border-strong);
}
.hoverband {
  fill: var(--bg-hover);
}
.selband {
  fill: var(--accent-soft);
  stroke: var(--accent);
  stroke-width: 1;
}
.tick {
  fill: var(--text-faint);
  font-family: var(--font-mono);
  font-size: 10px;
}
@media (max-width: 640px) {
  .hist__legend {
    display: none;
  }
}
</style>
