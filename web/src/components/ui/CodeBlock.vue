<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import Icon from './Icon.vue'
import { looksLikeDiff } from '@/lib/approval'

const props = withDefaults(
  defineProps<{
    text: string
    /** yaml | json | diff | text; "auto" detects diffs */
    format?: string
    /** expanded by default up to this many lines */
    openUpTo?: number
    /** lines shown while collapsed */
    peek?: number
    label?: string
  }>(),
  { format: 'auto', openUpTo: 30, peek: 10 },
)

const lines = computed(() => (props.text ?? '').replace(/\n$/, '').split('\n'))
const isDiff = computed(() => props.format === 'diff' || ((props.format === 'auto' || props.format === 'text') && looksLikeDiff(props.text ?? '')))
const long = computed(() => lines.value.length > props.openUpTo)
const open = ref(!long.value)
watch(long, (l) => (open.value = !l))
const shown = computed(() => (open.value ? lines.value : lines.value.slice(0, props.peek)))

function cls(l: string) {
  if (!isDiff.value) return ''
  if (l.startsWith('+++') || l.startsWith('---') || l.startsWith('diff ') || l.startsWith('index ')) return 'cb__l--meta'
  if (l.startsWith('@@')) return 'cb__l--hunk'
  if (l.startsWith('+')) return 'cb__l--add'
  if (l.startsWith('-')) return 'cb__l--del'
  return ''
}
</script>

<template>
  <div class="cb" :class="{ 'cb--diff': isDiff }">
    <div v-if="label || long" class="cb__bar mono">
      <span v-if="label" class="cb__label">{{ label }}</span>
      <span class="cb__fmt">{{ isDiff ? 'diff' : format === 'auto' ? '' : format }} · {{ lines.length }} line{{ lines.length > 1 ? 's' : '' }}</span>
      <button v-if="long" class="cb__toggle" :aria-expanded="open" @click="open = !open">
        <Icon :name="open ? 'chevronDown' : 'chevronRight'" :size="11" />{{ open ? 'Collapse' : `Show all ${lines.length} lines` }}
      </button>
    </div>
    <pre class="cb__pre mono"><code><span v-for="(l, i) in shown" :key="i" class="cb__l" :class="cls(l)">{{ l || ' ' }}</span></code></pre>
    <button v-if="long && !open" class="cb__more mono" @click="open = true">… {{ lines.length - peek }} more lines</button>
  </div>
</template>

<style scoped>
.cb {
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--code-bg);
  overflow: hidden;
}
.cb__bar {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 4px 10px;
  border-bottom: 1px solid var(--border);
  font-size: var(--fs-xs);
  color: var(--text-faint);
}
.cb__label {
  color: var(--text-dim);
  font-weight: 500;
}
.cb__toggle {
  margin-left: auto;
  display: inline-flex;
  align-items: center;
  gap: 3px;
  border: 0;
  background: none;
  color: var(--text-dim);
  font: inherit;
  padding: 0;
}
.cb__toggle:hover {
  color: var(--text-strong);
}
.cb__pre {
  margin: 0;
  padding: 8px 0;
  font-size: var(--fs-xs);
  line-height: 1.55;
  overflow-x: auto;
  max-height: 520px;
}
.cb__l {
  display: block;
  padding: 0 10px;
  white-space: pre;
  color: var(--text);
}
.cb__l--add {
  background: color-mix(in srgb, var(--success) 12%, transparent);
  color: color-mix(in srgb, var(--success) 80%, var(--text-strong));
}
.cb__l--del {
  background: var(--danger-soft);
  color: color-mix(in srgb, var(--danger) 80%, var(--text-strong));
}
.cb__l--hunk {
  color: var(--sev-info);
  background: var(--sev-info-soft);
}
.cb__l--meta {
  color: var(--text-faint);
}
.cb__more {
  display: block;
  width: 100%;
  border: 0;
  border-top: 1px dashed var(--border);
  background: none;
  padding: 4px 10px;
  text-align: left;
  font-size: var(--fs-xs);
  color: var(--text-dim);
}
.cb__more:hover {
  color: var(--text-strong);
  background: var(--bg-hover);
}
</style>
