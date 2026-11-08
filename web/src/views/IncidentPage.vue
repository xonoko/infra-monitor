<script setup lang="ts">
import { useRouter } from 'vue-router'
import IncidentView from '@/components/incident/IncidentView.vue'
import Btn from '@/components/ui/Btn.vue'

defineProps<{ id: number }>()
const router = useRouter()
</script>

<template>
  <div class="ip">
    <div class="ip__bar">
      <Btn variant="ghost" size="sm" icon="chevronRight" class="ip__back" @click="router.push({ path: '/incidents', query: { incident: String(id) } })">Back to incidents</Btn>
    </div>
    <div class="ip__view"><IncidentView :id="id" mode="page" @close="router.push('/incidents')" /></div>
  </div>
</template>

<style scoped>
.ip {
  display: flex;
  flex-direction: column;
  height: 100%;
}
.ip__bar {
  display: flex;
  align-items: center;
  height: 40px;
  padding: 0 12px;
  border-bottom: 1px solid var(--border);
  background: var(--bg-elev);
  flex: none;
}
.ip__back :deep(svg) {
  transform: rotate(180deg);
}
.ip__view {
  flex: 1;
  min-height: 0;
  max-width: 1100px;
  width: 100%;
  margin: 0 auto;
  border-left: 1px solid var(--border);
  border-right: 1px solid var(--border);
}
</style>
