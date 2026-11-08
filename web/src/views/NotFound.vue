<script setup lang="ts">
import EmptyState from '@/components/ui/EmptyState.vue'
import Btn from '@/components/ui/Btn.vue'
</script>

<template>
  <EmptyState icon="search" title="Page not found" text="This page does not exist.">
    <RouterLink to="/incidents"><Btn>Go to incidents</Btn></RouterLink>
  </EmptyState>
</template>
