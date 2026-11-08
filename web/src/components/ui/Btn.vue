<script setup lang="ts">
import Icon from './Icon.vue'

withDefaults(
  defineProps<{
    variant?: 'default' | 'primary' | 'danger' | 'ghost' | 'subtle'
    size?: 'sm' | 'md'
    icon?: string
    loading?: boolean
    disabled?: boolean
    type?: 'button' | 'submit'
    title?: string
  }>(),
  { variant: 'default', size: 'md', type: 'button' },
)
</script>

<template>
  <button
    :type="type"
    class="btn"
    :class="[`btn--${variant}`, `btn--${size}`, { 'btn--loading': loading }]"
    :disabled="disabled || loading"
    :title="title"
  >
    <span v-if="loading" class="spin" aria-hidden="true" />
    <Icon v-else-if="icon" :name="icon" :size="size === 'sm' ? 13 : 15" />
    <span v-if="$slots.default" class="btn__label"><slot /></span>
  </button>
</template>

<style scoped>
.btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  border: 1px solid var(--border-strong);
  background: var(--bg-panel);
  color: var(--text-strong);
  border-radius: var(--radius);
  font-weight: 500;
  white-space: nowrap;
  transition: background var(--dur) var(--ease), border-color var(--dur) var(--ease), color var(--dur) var(--ease);
  user-select: none;
}
.btn--md {
  height: 30px;
  padding: 0 12px;
  font-size: var(--fs-md);
}
.btn--sm {
  height: 24px;
  padding: 0 8px;
  font-size: var(--fs-sm);
  gap: 5px;
}
.btn:hover:not(:disabled) {
  background: var(--bg-hover);
  border-color: var(--text-faint);
}
.btn:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}
.btn--primary {
  background: var(--accent);
  border-color: var(--accent);
  color: var(--text-inverse);
}
.btn--primary:hover:not(:disabled) {
  background: var(--accent-strong);
  border-color: var(--accent-strong);
}
.btn--danger {
  color: var(--danger);
  border-color: color-mix(in srgb, var(--danger) 45%, var(--border-strong));
  background: var(--danger-soft);
}
.btn--danger:hover:not(:disabled) {
  background: color-mix(in srgb, var(--danger) 22%, transparent);
  border-color: var(--danger);
}
.btn--ghost {
  background: transparent;
  border-color: transparent;
  color: var(--text-dim);
}
.btn--ghost:hover:not(:disabled) {
  background: var(--bg-hover);
  color: var(--text-strong);
  border-color: transparent;
}
.btn--subtle {
  background: var(--bg-hover);
  border-color: transparent;
  color: var(--text);
}
.spin {
  width: 12px;
  height: 12px;
  border-radius: 50%;
  border: 2px solid currentColor;
  border-right-color: transparent;
  animation: spin 0.7s linear infinite;
}
@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
