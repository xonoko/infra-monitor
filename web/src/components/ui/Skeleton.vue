<script setup lang="ts">
withDefaults(defineProps<{ lines?: number; height?: number }>(), { lines: 3, height: 14 })
</script>

<template>
  <div class="sk" aria-busy="true" aria-label="Loading">
    <div v-for="i in lines" :key="i" class="sk__line" :style="{ height: `${height}px`, width: `${92 - ((i * 17) % 40)}%` }" />
  </div>
</template>

<style scoped>
.sk {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 4px 0;
}
.sk__line {
  border-radius: 4px;
  background: linear-gradient(90deg, var(--bg-hover) 0%, var(--bg-active) 50%, var(--bg-hover) 100%);
  background-size: 200% 100%;
  animation: shimmer 1.4s ease-in-out infinite;
}
@keyframes shimmer {
  from {
    background-position: 100% 0;
  }
  to {
    background-position: -100% 0;
  }
}
</style>
