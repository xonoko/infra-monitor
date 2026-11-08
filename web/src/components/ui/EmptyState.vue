<script setup lang="ts">
import Icon from './Icon.vue'
defineProps<{ icon?: string; title: string; text?: string }>()
</script>

<template>
  <div class="empty">
    <div class="empty__icon"><Icon :name="icon ?? 'check'" :size="20" /></div>
    <div class="empty__title">{{ title }}</div>
    <p v-if="text" class="empty__text">{{ text }}</p>
    <div v-if="$slots.default" class="empty__actions"><slot /></div>
  </div>
</template>

<style scoped>
.empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  text-align: center;
  padding: 48px 16px;
  color: var(--text-dim);
}
.empty__icon {
  width: 40px;
  height: 40px;
  border-radius: 10px;
  display: grid;
  place-items: center;
  background: var(--bg-hover);
  color: var(--text-dim);
  margin-bottom: 12px;
}
.empty__title {
  color: var(--text-strong);
  font-weight: 600;
}
.empty__text {
  margin: 6px 0 0;
  max-width: 380px;
}
.empty__actions {
  margin-top: 14px;
  display: flex;
  gap: 8px;
}
</style>
