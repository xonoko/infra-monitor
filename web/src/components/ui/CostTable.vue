<script setup lang="ts">
import type { AICost, AICostLine } from '@/api/types'
import Tip from './Tip.vue'
import { tok, usd } from '@/lib/format'

defineProps<{ cost?: AICost | null }>()

const ROWS: { key: keyof AICost; label: string; hint: string }[] = [
  { key: 'investigation', label: 'Investigation', hint: 'automatic, manual and deep investigations' },
  { key: 'remediation', label: 'Remediation', hint: 'investigations triggered by actions (verification after a fix)' },
  { key: 'chat', label: 'Chat', hint: 'agent replies' },
  { key: 'total', label: 'Total', hint: 'all AI calls' },
]

const empty: AICostLine = { calls: 0, prompt_tokens: 0, completion_tokens: 0, usd: 0 }
const tip = (l: AICostLine, hint: string) =>
  l.calls
    ? `${l.calls} call${l.calls > 1 ? 's' : ''} · ${l.prompt_tokens.toLocaleString()} tokens in / ${l.completion_tokens.toLocaleString()} out — ${hint}`
    : `No call — ${hint}`
</script>

<template>
  <dl class="ct">
    <template v-for="r in ROWS" :key="r.key">
      <Tip :text="tip(cost?.[r.key] ?? empty, r.hint)" side="left">
        <div class="ct__row" :class="{ 'ct__row--total': r.key === 'total', 'ct__row--zero': !(cost?.[r.key]?.calls) }" tabindex="0">
          <dt>{{ r.label }}</dt>
          <dd class="ct__tok mono">
            <template v-if="cost?.[r.key]?.calls">{{ tok(cost[r.key].prompt_tokens) }}/{{ tok(cost[r.key].completion_tokens) }} · {{ cost[r.key].calls }}×</template>
          </dd>
          <dd class="ct__usd mono">{{ usd(cost?.[r.key]?.usd, cost?.[r.key]?.calls ?? 0) }}</dd>
        </div>
      </Tip>
    </template>
  </dl>
</template>

<style scoped>
.ct {
  margin: 0;
  display: flex;
  flex-direction: column;
  font-size: var(--fs-sm);
}
.ct__row {
  display: grid;
  grid-template-columns: 1fr auto 72px;
  align-items: baseline;
  gap: 8px;
  padding: 2px 4px;
  margin: 0 -4px;
  border-radius: 4px;
  outline: none;
}
.ct__row:hover,
.ct__row:focus-visible {
  background: var(--bg-hover);
}
.ct__row dt {
  color: var(--text-dim);
}
.ct__row dd {
  margin: 0;
}
.ct__tok {
  font-size: var(--fs-xs);
  color: var(--text-faint);
  white-space: nowrap;
}
.ct__usd {
  text-align: right;
  color: var(--text);
  white-space: nowrap;
}
.ct__row--zero .ct__usd {
  color: var(--text-faint);
}
.ct__row--total {
  border-top: 1px solid var(--border);
  border-radius: 0 0 4px 4px;
  margin-top: 3px;
  padding-top: 5px;
}
.ct__row--total dt,
.ct__row--total .ct__usd {
  color: var(--text-strong);
  font-weight: 600;
}
</style>
