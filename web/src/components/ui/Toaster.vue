<script setup lang="ts">
import { ToastClose, ToastDescription, ToastProvider, ToastRoot, ToastTitle, ToastViewport } from 'reka-ui'
import { dismiss, toasts } from '@/lib/toast'
import Icon from './Icon.vue'
</script>

<template>
  <ToastProvider :duration="5000" swipe-direction="right">
    <ToastRoot
      v-for="t in toasts"
      :key="t.id"
      class="toast"
      :class="`toast--${t.kind}`"
      :duration="t.kind === 'error' ? 8000 : 4000"
      @update:open="(o: boolean) => !o && dismiss(t.id)"
    >
      <span class="toast__bar" aria-hidden="true" />
      <div class="toast__main">
        <ToastTitle class="toast__title">{{ t.title }}</ToastTitle>
        <ToastDescription v-if="t.description" class="toast__desc">{{ t.description }}</ToastDescription>
      </div>
      <ToastClose class="toast__x" aria-label="Dismiss"><Icon name="x" :size="14" /></ToastClose>
    </ToastRoot>
    <ToastViewport class="toast-viewport" />
  </ToastProvider>
</template>

<style>
.toast-viewport {
  position: fixed;
  bottom: 16px;
  right: 16px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: min(380px, calc(100vw - 32px));
  margin: 0;
  padding: 0;
  list-style: none;
  z-index: 200;
  outline: none;
}
.toast {
  position: relative;
  display: flex;
  gap: 10px;
  align-items: flex-start;
  background: var(--bg-elev);
  border-radius: var(--radius);
  box-shadow: var(--shadow-pop);
  padding: 10px 10px 10px 14px;
  overflow: hidden;
  animation: toast-in 180ms var(--ease);
}
.toast__bar {
  position: absolute;
  left: 0;
  top: 0;
  bottom: 0;
  width: 3px;
  background: var(--sev-info);
}
.toast--success .toast__bar {
  background: var(--success);
}
.toast--error .toast__bar {
  background: var(--danger);
}
.toast__main {
  flex: 1;
  min-width: 0;
}
.toast__title {
  font-weight: 600;
  color: var(--text-strong);
}
.toast__desc {
  color: var(--text-dim);
  font-size: var(--fs-sm);
  margin-top: 2px;
  overflow-wrap: anywhere;
}
.toast__x {
  border: 0;
  background: none;
  color: var(--text-faint);
  padding: 2px;
}
.toast[data-state='closed'] {
  animation: toast-out 120ms ease-in forwards;
}
@keyframes toast-in {
  from {
    opacity: 0;
    transform: translateY(8px);
  }
}
@keyframes toast-out {
  to {
    opacity: 0;
    transform: translateX(20px);
  }
}
</style>
