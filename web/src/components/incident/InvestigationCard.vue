<script setup lang="ts">
import { computed, ref } from 'vue'
import type { Investigation } from '@/api/types'
import Chip from '../ui/Chip.vue'
import Icon from '../ui/Icon.vue'
import Markdown from '../ui/Markdown.vue'
import { duration, pct } from '@/lib/format'
import { useMe } from '@/composables/useMe'

const props = withDefaults(
  defineProps<{
    inv?: Investigation | null
    /** fallback when the investigation row is not loaded (timeline payload) */
    fallback?: { verdict?: string; confidence?: number; summary?: string }
    defaultOpen?: boolean
  }>(),
  { defaultOpen: false },
)
const { traceURL } = useMe()
const open = ref(props.defaultOpen)

const verdict = computed(() => props.inv?.verdict ?? props.fallback?.verdict ?? 'unknown')
const confidence = computed(() => props.inv?.confidence ?? props.fallback?.confidence ?? 0)
const summary = computed(() => props.inv?.summary || props.fallback?.summary || '')
const took = computed(() => {
  const i = props.inv
  if (!i?.started_at || !i.finished_at) return ''
  return duration((new Date(i.finished_at).getTime() - new Date(i.started_at).getTime()) / 1000)
})
const hasMore = computed(() => !!(props.inv?.root_cause || props.inv?.analysis))
const confColor = computed(() => (confidence.value >= 0.75 ? 'var(--success)' : confidence.value >= 0.45 ? 'var(--warning)' : 'var(--sev-other)'))
const tools = computed(() => (Array.isArray(props.inv?.tool_calls) ? props.inv!.tool_calls!.length : 0))
</script>

<template>
  <article class="ic">
    <header class="ic__head">
      <span class="ic__badge"><Icon name="sparkle" :size="13" />AI investigation</span>
      <Chip kind="verdict" :value="verdict" dense />
      <span class="ic__conf" :title="`confidence ${pct(confidence)}`">
        <span class="ic__bar"><span :style="{ width: pct(confidence), background: confColor }" /></span>
        <span class="mono">{{ pct(confidence) }}</span>
      </span>
      <span class="ic__meta mono">
        <template v-if="inv?.model">{{ inv.model }}</template>
        <template v-if="inv?.trigger && inv.trigger !== 'auto'"> · {{ inv.trigger }}</template>
        <template v-if="took"> · {{ took }}</template>
        <template v-if="tools"> · {{ tools }} tool calls</template>
        <template v-if="inv?.prompt_tokens"> · {{ (inv.prompt_tokens + inv.completion_tokens).toLocaleString() }} tok</template>
      </span>
      <a v-if="inv?.trace_id && traceURL(inv.trace_id)" :href="traceURL(inv.trace_id)" target="_blank" rel="noopener" class="ic__trace mono"><Icon name="external" :size="11" />trace</a>
    </header>
    <p v-if="summary" class="ic__summary">{{ summary }}</p>
    <button v-if="hasMore" class="ic__toggle" :aria-expanded="open" @click="open = !open">
      <Icon :name="open ? 'chevronDown' : 'chevronRight'" :size="13" />{{ open ? 'Hide' : 'Root cause and full analysis' }}
    </button>
    <div v-if="open && hasMore" class="ic__more">
      <template v-if="inv?.root_cause">
        <div class="section-title">Root cause</div>
        <Markdown :source="inv.root_cause" />
      </template>
      <template v-if="inv?.analysis">
        <div class="section-title ic__st">Analysis</div>
        <Markdown :source="inv.analysis" />
      </template>
    </div>
  </article>
</template>

<style scoped>
.ic {
  border: 1px solid color-mix(in srgb, var(--accent) 28%, var(--border));
  border-radius: var(--radius-lg);
  background: linear-gradient(180deg, color-mix(in srgb, var(--accent) 6%, var(--bg-panel)), var(--bg-panel) 70%);
  padding: 10px 12px;
}
.ic__head {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px 10px;
}
.ic__badge {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  color: var(--accent);
  font-weight: 600;
  font-size: var(--fs-sm);
}
.ic__conf {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: var(--fs-xs);
}
.ic__bar {
  width: 56px;
  height: 4px;
  border-radius: 2px;
  background: var(--bg-active);
  overflow: hidden;
}
.ic__bar span {
  display: block;
  height: 100%;
}
.ic__meta {
  font-size: var(--fs-xs);
  color: var(--text-faint);
}
.ic__trace {
  margin-left: auto;
  display: inline-flex;
  align-items: center;
  gap: 3px;
  font-size: var(--fs-xs);
}
.ic__summary {
  margin: 8px 0 0;
  color: var(--text-strong);
  font-size: var(--fs-md);
  line-height: 1.5;
}
.ic__toggle {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  border: 0;
  background: none;
  padding: 6px 0 0;
  color: var(--text-dim);
  font-size: var(--fs-sm);
  font-weight: 500;
}
.ic__toggle:hover {
  color: var(--text-strong);
}
.ic__more {
  margin-top: 8px;
  padding: 10px 12px;
  border-radius: var(--radius);
  background: var(--bg-elev);
  border: 1px solid var(--border);
  font-size: var(--fs-md);
}
.ic__more .section-title {
  margin-bottom: 4px;
}
.ic__st {
  margin-top: 12px;
}
</style>
