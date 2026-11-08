<script setup lang="ts">
import { computed } from 'vue'
const props = defineProps<{ labels: Record<string, string>; hide?: string[]; max?: number }>()
const entries = computed(() => {
  const hidden = new Set(props.hide ?? [])
  const all = Object.entries(props.labels ?? {}).filter(([k]) => !hidden.has(k))
  all.sort(([a], [b]) => a.localeCompare(b))
  return props.max ? all.slice(0, props.max) : all
})
const more = computed(() => Object.keys(props.labels ?? {}).length - (props.hide?.length ?? 0) - entries.value.length)
</script>

<template>
  <div class="labels">
    <span v-for="[k, v] in entries" :key="k" class="lbl" :title="`${k}=${v}`">
      <span class="lbl__k">{{ k }}</span><span class="lbl__eq">=</span><span class="lbl__v">{{ v }}</span>
    </span>
    <span v-if="more > 0" class="lbl lbl--more">+{{ more }}</span>
  </div>
</template>

<style scoped>
.labels {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}
.lbl {
  display: inline-flex;
  max-width: 100%;
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  line-height: 18px;
  padding: 0 6px;
  border-radius: 4px;
  background: var(--bg-hover);
  border: 1px solid var(--border);
  overflow: hidden;
}
.lbl__k {
  color: var(--text-dim);
}
.lbl__eq {
  color: var(--text-faint);
}
.lbl__v {
  color: var(--text-strong);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.lbl--more {
  color: var(--text-dim);
}
</style>
