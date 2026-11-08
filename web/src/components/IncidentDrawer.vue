<script setup lang="ts">
import { computed } from 'vue'
import { DialogContent, DialogPortal, DialogRoot, DialogTitle } from 'reka-ui'
import IncidentView from './incident/IncidentView.vue'

const props = defineProps<{ id: number | null }>()
const emit = defineEmits<{ close: []; expand: [id: number] }>()
const open = computed({
  get: () => props.id !== null,
  set: (v) => {
    if (!v) emit('close')
  },
})
</script>

<template>
  <DialogRoot v-model:open="open" :modal="false">
    <DialogPortal>
      <DialogContent
        class="drawer"
        :aria-describedby="undefined"
        @interact-outside.prevent
        @pointer-down-outside.prevent
        @focus-outside.prevent
        @open-auto-focus.prevent
      >
        <DialogTitle class="sr-only">Incident #{{ id }}</DialogTitle>
        <IncidentView v-if="id !== null" :id="id" mode="drawer" @close="emit('close')" @expand="emit('expand', id)" />
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>

<style>
.drawer {
  position: fixed;
  top: 0;
  right: 0;
  bottom: 0;
  width: var(--drawer-w);
  min-width: 420px;
  background: var(--bg-elev);
  border-left: 1px solid var(--border-strong);
  box-shadow: var(--shadow-drawer);
  z-index: 50;
  outline: none;
  animation: drawer-in 200ms var(--ease);
}
.drawer[data-state='closed'] {
  animation: drawer-out 140ms ease-in forwards;
}
@keyframes drawer-in {
  from {
    transform: translateX(24px);
    opacity: 0;
  }
}
@keyframes drawer-out {
  to {
    transform: translateX(24px);
    opacity: 0;
  }
}
@media (max-width: 900px) {
  .drawer {
    width: 100vw;
    min-width: 0;
    border-left: 0;
  }
}
</style>
