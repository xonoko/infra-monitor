<script setup lang="ts">
import { DialogClose, DialogContent, DialogDescription, DialogOverlay, DialogPortal, DialogRoot, DialogTitle } from 'reka-ui'
import Icon from './Icon.vue'

const open = defineModel<boolean>('open', { required: true })
withDefaults(defineProps<{ title: string; description?: string; width?: number }>(), { width: 480 })
</script>

<template>
  <DialogRoot v-model:open="open">
    <DialogPortal>
      <DialogOverlay class="modal-scrim" />
      <DialogContent class="modal" :style="{ width: `min(${width}px, calc(100vw - 24px))` }">
        <header class="modal__head">
          <div>
            <DialogTitle class="modal__title">{{ title }}</DialogTitle>
            <DialogDescription v-if="description" class="modal__desc">{{ description }}</DialogDescription>
          </div>
          <DialogClose class="modal__x" aria-label="Close"><Icon name="x" /></DialogClose>
        </header>
        <div class="modal__body"><slot /></div>
        <footer v-if="$slots.footer" class="modal__foot"><slot name="footer" /></footer>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>

<style>
.modal-scrim {
  position: fixed;
  inset: 0;
  background: var(--scrim);
  z-index: 60;
  animation: fade 140ms var(--ease);
}
.modal {
  position: fixed;
  top: 12vh;
  left: 50%;
  transform: translateX(-50%);
  max-height: 76vh;
  display: flex;
  flex-direction: column;
  background: var(--bg-elev);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-pop);
  z-index: 61;
  animation: pop 160ms var(--ease);
}
.modal__head {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 12px;
  padding: 16px 18px 10px;
}
.modal__title {
  font-size: var(--fs-lg);
  font-weight: 600;
  color: var(--text-strong);
  margin: 0;
}
.modal__desc {
  margin: 4px 0 0;
  color: var(--text-dim);
}
.modal__x {
  border: 0;
  background: transparent;
  color: var(--text-dim);
  padding: 4px;
  border-radius: 4px;
}
.modal__x:hover {
  background: var(--bg-hover);
  color: var(--text-strong);
}
.modal__body {
  padding: 6px 18px 16px;
  overflow: auto;
}
.modal__foot {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding: 12px 18px;
  border-top: 1px solid var(--border);
}
@keyframes fade {
  from {
    opacity: 0;
  }
}
@keyframes pop {
  from {
    opacity: 0;
    transform: translateX(-50%) translateY(6px) scale(0.98);
  }
}
</style>
