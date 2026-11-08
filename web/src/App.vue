<script setup lang="ts">
import { computed, onBeforeUnmount, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useQueryClient } from '@tanstack/vue-query'
import { TooltipProvider } from 'reka-ui'
import Sidebar from './components/Sidebar.vue'
import Toaster from './components/ui/Toaster.vue'
import { useMe } from './composables/useMe'
import { startGlobalStream } from './api/sse'

const route = useRoute()
const isPublic = computed(() => !!route.meta.public)
const qc = useQueryClient()
const { me } = useMe()

let stop: (() => void) | null = null
watch(
  () => !!me.value && !isPublic.value,
  (on) => {
    if (on && !stop) stop = startGlobalStream(qc)
    if (!on && stop) {
      stop()
      stop = null
    }
  },
  { immediate: true },
)
onBeforeUnmount(() => stop?.())
</script>

<template>
  <TooltipProvider>
    <RouterView v-if="isPublic" />
    <div v-else class="shell">
      <Sidebar />
      <main class="shell__main">
        <RouterView />
      </main>
    </div>
    <Toaster />
  </TooltipProvider>
</template>

<style scoped>
.shell {
  display: grid;
  grid-template-columns: var(--sidebar-w) minmax(0, 1fr);
  height: 100%;
}
.shell__main {
  min-width: 0;
  height: 100%;
  overflow: auto;
  position: relative;
}
@media (max-width: 640px) {
  .shell {
    grid-template-columns: 1fr;
    grid-template-rows: minmax(0, 1fr) auto;
  }
  .shell__main {
    grid-row: 1;
  }
}
</style>
