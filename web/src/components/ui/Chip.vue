<script setup lang="ts">
import { computed } from 'vue'
import { sevKey } from '@/lib/format'

const props = defineProps<{ kind: 'status' | 'severity' | 'verdict' | 'risk' | 'action' | 'plain'; value: string; dense?: boolean }>()

const tone = computed(() => {
  const v = (props.value || '').toLowerCase()
  switch (props.kind) {
    case 'severity':
      return sevKey(v)
    case 'status':
      return ({ open: 'open', acknowledged: 'ack', resolved: 'resolved', closed: 'closed' } as Record<string, string>)[v] ?? 'closed'
    case 'verdict':
      return ({ actionable: 'warning', noise: 'other', unknown: 'faint' } as Record<string, string>)[v] ?? 'faint'
    case 'risk':
      return ({ low: 'info', medium: 'warning', high: 'critical' } as Record<string, string>)[v] ?? 'other'
    case 'action':
      return (
        (
          {
            proposed: 'info',
            approved: 'ack',
            running: 'ack',
            succeeded: 'resolved',
            failed: 'critical',
            rejected: 'other',
            expired: 'faint',
          } as Record<string, string>
        )[v] ?? 'other'
      )
    default:
      return 'faint'
  }
})

const label = computed(() => (props.kind === 'status' && props.value === 'acknowledged' ? 'ack' : props.value || '—'))
</script>

<template>
  <span class="chip" :class="[`chip--${tone}`, { 'chip--dense': dense }]">
    <span class="chip__dot" aria-hidden="true" />
    {{ label }}
  </span>
</template>

<style scoped>
.chip {
  --c: var(--text-dim);
  display: inline-flex;
  align-items: center;
  gap: 5px;
  height: 20px;
  padding: 0 7px;
  border-radius: 4px;
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  font-weight: 500;
  letter-spacing: 0.01em;
  color: var(--c);
  background: color-mix(in srgb, var(--c) 12%, transparent);
  border: 1px solid color-mix(in srgb, var(--c) 22%, transparent);
  white-space: nowrap;
  text-transform: lowercase;
}
.chip--dense {
  height: 18px;
  padding: 0 5px;
}
.chip__dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--c);
}
.chip--critical {
  --c: var(--sev-critical);
}
.chip--warning {
  --c: var(--sev-warning);
}
.chip--info {
  --c: var(--sev-info);
}
.chip--other {
  --c: var(--sev-other);
}
.chip--faint {
  --c: var(--text-faint);
}
.chip--open {
  --c: var(--st-open);
}
.chip--ack {
  --c: var(--st-ack);
}
.chip--resolved {
  --c: var(--st-resolved);
}
.chip--closed {
  --c: var(--st-closed);
}
</style>
