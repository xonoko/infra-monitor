<script setup lang="ts">
import { ref, watch } from 'vue'
import { PopoverClose, PopoverContent, PopoverPortal, PopoverRoot, PopoverTrigger } from 'reka-ui'
import Icon from './ui/Icon.vue'
import Btn from './ui/Btn.vue'
import { RANGE_PRESETS, rangeLabel, type TimeRange } from '@/lib/search'

const model = defineModel<TimeRange>({ required: true })
const open = ref(false)

function toLocalInput(iso?: string) {
  const d = iso ? new Date(iso) : new Date()
  const off = d.getTimezoneOffset() * 60000
  return new Date(d.getTime() - off).toISOString().slice(0, 16)
}
const since = ref('')
const until = ref('')
watch(open, (o) => {
  if (!o) return
  since.value = toLocalInput(model.value.since ?? new Date(Date.now() - 86400e3).toISOString())
  until.value = model.value.until ? toLocalInput(model.value.until) : ''
})

function pick(id: string) {
  model.value = { preset: id }
  open.value = false
}
function applyCustom() {
  if (!since.value) return
  model.value = {
    since: new Date(since.value).toISOString(),
    until: until.value ? new Date(until.value).toISOString() : undefined,
  }
  open.value = false
}
</script>

<template>
  <PopoverRoot v-model:open="open">
    <PopoverTrigger class="trp" aria-label="Time range">
      <Icon name="clock" :size="14" />
      <span class="trp__label">{{ rangeLabel(model) }}</span>
      <Icon name="chevronDown" :size="13" class="faint" />
    </PopoverTrigger>
    <PopoverPortal>
      <PopoverContent class="trp-pop" align="end" :side-offset="6">
        <div class="trp-pop__presets">
          <button
            v-for="p in RANGE_PRESETS"
            :key="p.id"
            class="trp-pop__preset"
            :class="{ 'trp-pop__preset--on': model.preset === p.id }"
            @click="pick(p.id)"
          >
            <span>{{ p.label }}</span><span class="mono faint">{{ p.id }}</span>
          </button>
        </div>
        <form class="trp-pop__custom" @submit.prevent="applyCustom">
          <div class="section-title">Custom range</div>
          <label class="label" for="trp-since">From</label>
          <input id="trp-since" v-model="since" type="datetime-local" class="input mono" required />
          <label class="label" for="trp-until">To <span class="faint">(empty = now)</span></label>
          <input id="trp-until" v-model="until" type="datetime-local" class="input mono" />
          <div class="trp-pop__actions">
            <PopoverClose as-child><Btn variant="ghost" size="sm">Cancel</Btn></PopoverClose>
            <Btn variant="primary" size="sm" type="submit">Apply</Btn>
          </div>
        </form>
      </PopoverContent>
    </PopoverPortal>
  </PopoverRoot>
</template>

<style scoped>
.trp {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  height: 32px;
  padding: 0 10px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius);
  background: var(--bg-input);
  color: var(--text-strong);
  font-size: var(--fs-sm);
  white-space: nowrap;
}
.trp:hover {
  border-color: var(--text-faint);
}
@media (max-width: 640px) {
  .trp__label {
    display: none;
  }
}
</style>

<style>
.trp-pop {
  display: grid;
  grid-template-columns: 190px 230px;
  background: var(--bg-elev);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-pop);
  z-index: 70;
  overflow: hidden;
}
.trp-pop__presets {
  padding: 6px;
  border-right: 1px solid var(--border);
  display: flex;
  flex-direction: column;
}
.trp-pop__preset {
  display: flex;
  justify-content: space-between;
  border: 0;
  background: none;
  padding: 7px 10px;
  border-radius: 5px;
  text-align: left;
  color: var(--text);
}
.trp-pop__preset:hover {
  background: var(--bg-hover);
}
.trp-pop__preset--on {
  background: var(--accent-soft);
  color: var(--accent);
}
.trp-pop__custom {
  padding: 12px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.trp-pop__custom .label {
  margin: 8px 0 2px;
}
.trp-pop__actions {
  display: flex;
  justify-content: flex-end;
  gap: 6px;
  margin-top: 12px;
}
@media (max-width: 520px) {
  .trp-pop {
    grid-template-columns: 1fr;
  }
  .trp-pop__presets {
    border-right: 0;
    border-bottom: 1px solid var(--border);
  }
}
</style>
