<script setup lang="ts">
import { TooltipContent, TooltipPortal, TooltipRoot, TooltipTrigger } from 'reka-ui'
withDefaults(defineProps<{ text: string; side?: 'top' | 'right' | 'bottom' | 'left' }>(), { side: 'top' })
</script>

<template>
  <TooltipRoot :delay-duration="250">
    <TooltipTrigger as-child>
      <slot />
    </TooltipTrigger>
    <TooltipPortal>
      <TooltipContent class="tip" :side="side" :side-offset="6">{{ text }}</TooltipContent>
    </TooltipPortal>
  </TooltipRoot>
</template>

<style>
.tip {
  z-index: 100;
  background: var(--bg-elev);
  color: var(--text-strong);
  border-radius: 5px;
  padding: 4px 8px;
  font-size: var(--fs-sm);
  box-shadow: var(--shadow-pop);
  max-width: 320px;
}
</style>
