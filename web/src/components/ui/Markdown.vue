<script setup lang="ts">
import { computed } from 'vue'
import { renderMarkdown } from '@/lib/markdown'
const props = defineProps<{ source: string }>()
const html = computed(() => renderMarkdown(props.source))
</script>

<template>
  <!-- eslint-disable-next-line vue/no-v-html -- sanitized with DOMPurify -->
  <div class="md" v-html="html" />
</template>

<style scoped>
.md {
  line-height: 1.6;
  overflow-wrap: anywhere;
}
.md :deep(p) {
  margin: 0 0 0.6em;
}
.md :deep(p:last-child) {
  margin-bottom: 0;
}
.md :deep(h1),
.md :deep(h2),
.md :deep(h3),
.md :deep(h4) {
  font-size: var(--fs-md);
  margin: 1em 0 0.4em;
  color: var(--text-strong);
}
.md :deep(h1:first-child),
.md :deep(h2:first-child),
.md :deep(h3:first-child) {
  margin-top: 0;
}
.md :deep(ul),
.md :deep(ol) {
  margin: 0 0 0.6em;
  padding-left: 1.3em;
}
.md :deep(li) {
  margin: 0.15em 0;
}
.md :deep(code) {
  background: var(--code-bg);
  border: 1px solid var(--border);
  border-radius: 4px;
  padding: 0 4px;
  font-size: 0.88em;
}
.md :deep(pre) {
  background: var(--code-bg);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 10px 12px;
  overflow-x: auto;
  margin: 0 0 0.7em;
}
.md :deep(pre code) {
  background: none;
  border: 0;
  padding: 0;
  font-size: var(--fs-sm);
}
.md :deep(blockquote) {
  margin: 0 0 0.6em;
  padding-left: 10px;
  border-left: 2px solid var(--border-strong);
  color: var(--text-dim);
}
.md :deep(table) {
  border-collapse: collapse;
  font-size: var(--fs-sm);
  margin-bottom: 0.7em;
}
.md :deep(th),
.md :deep(td) {
  border: 1px solid var(--border);
  padding: 4px 8px;
}
</style>
