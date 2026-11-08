<script setup lang="ts">
import { ref } from 'vue'
import type { Signal } from '@/api/types'
import Chip from '../ui/Chip.vue'
import Icon from '../ui/Icon.vue'
import LabelList from '../ui/LabelList.vue'
import Skeleton from '../ui/Skeleton.vue'
import EmptyState from '../ui/EmptyState.vue'
import { absTime, relTime, sevKey } from '@/lib/format'

defineProps<{ signals?: Signal[]; loading?: boolean }>()
const open = ref<Record<number, boolean>>({})
const HIDE = ['alertname', 'severity', 'cluster', 'namespace']
</script>

<template>
  <Skeleton v-if="loading" :lines="6" />
  <EmptyState v-else-if="!signals?.length" icon="bell" title="No signals" />
  <div v-else class="sig">
    <article v-for="s in signals" :key="s.id" class="sig__item" :class="{ 'sig__item--res': s.status === 'resolved' }">
      <button class="sig__row" :aria-expanded="!!open[s.id]" @click="open[s.id] = !open[s.id]">
        <span class="sig__sev" :class="`sig__sev--${sevKey(s.severity)}`" />
        <Icon :name="open[s.id] ? 'chevronDown' : 'chevronRight'" :size="13" class="faint" />
        <span class="sig__name">{{ s.name || s.fingerprint }}</span>
        <Chip kind="status" :value="s.status === 'firing' ? 'open' : 'resolved'" dense />
        <span class="sig__where mono">{{ s.cluster }}<template v-if="s.namespace"> / {{ s.namespace }}</template></span>
        <span class="sig__time mono" :title="`started ${absTime(s.starts_at)}`">{{ relTime(s.starts_at) }}</span>
        <span v-if="s.fire_count > 1" class="sig__count mono" :title="`${s.fire_count} notifications`">×{{ s.fire_count }}</span>
      </button>
      <div v-if="s.annotations?.summary && !open[s.id]" class="sig__sum">{{ s.annotations.summary }}</div>
      <div v-if="open[s.id]" class="sig__detail">
        <div v-if="s.annotations && Object.keys(s.annotations).length" class="sig__ann">
          <div v-for="(v, k) in s.annotations" :key="k" class="sig__annrow">
            <span class="mono faint">{{ k }}</span>
            <span class="sig__annval">{{ v }}</span>
          </div>
        </div>
        <div class="section-title">Labels</div>
        <LabelList :labels="s.labels" :hide="HIDE" />
        <dl class="sig__dl mono">
          <dt>fingerprint</dt>
          <dd>{{ s.fingerprint }}</dd>
          <dt>source</dt>
          <dd>{{ s.source }}</dd>
          <dt>starts</dt>
          <dd>{{ absTime(s.starts_at) }}</dd>
          <dt>ends</dt>
          <dd>{{ s.ends_at ? absTime(s.ends_at) : '—' }}</dd>
          <dt>last seen</dt>
          <dd>{{ absTime(s.last_seen_at) }}</dd>
        </dl>
        <a v-if="s.generator_url" :href="s.generator_url" target="_blank" rel="noopener" class="sig__gen"><Icon name="external" :size="12" />Open in source</a>
      </div>
    </article>
  </div>
</template>

<style scoped>
.sig {
  display: flex;
  flex-direction: column;
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  overflow: hidden;
}
.sig__item + .sig__item {
  border-top: 1px solid var(--border);
}
.sig__item--res .sig__name {
  color: var(--text-dim);
}
.sig__row {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  border: 0;
  background: var(--bg-panel);
  padding: 8px 12px 8px 0;
  text-align: left;
  position: relative;
}
.sig__row:hover {
  background: var(--bg-hover);
}
.sig__sev {
  width: 3px;
  align-self: stretch;
  background: var(--sev-other);
  margin-right: 6px;
}
.sig__sev--critical {
  background: var(--sev-critical);
}
.sig__sev--warning {
  background: var(--sev-warning);
}
.sig__sev--info {
  background: var(--sev-info);
}
.sig__name {
  font-weight: 500;
  color: var(--text-strong);
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.sig__where,
.sig__time,
.sig__count {
  font-size: var(--fs-xs);
  color: var(--text-dim);
  white-space: nowrap;
}
.sig__sum {
  padding: 0 12px 8px 33px;
  font-size: var(--fs-sm);
  color: var(--text-dim);
  background: var(--bg-panel);
}
.sig__detail {
  padding: 4px 14px 14px 33px;
  background: var(--bg-panel);
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.sig__ann {
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: var(--fs-sm);
}
.sig__annrow {
  display: grid;
  grid-template-columns: 110px 1fr;
  gap: 8px;
}
.sig__annval {
  overflow-wrap: anywhere;
}
.sig__dl {
  display: grid;
  grid-template-columns: 90px 1fr;
  gap: 3px 8px;
  margin: 0;
  font-size: var(--fs-xs);
}
.sig__dl dt {
  color: var(--text-faint);
}
.sig__dl dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.sig__gen {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: var(--fs-sm);
}
@media (max-width: 520px) {
  .sig__where {
    display: none;
  }
}
</style>
