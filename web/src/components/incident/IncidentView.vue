<script setup lang="ts">
import { computed, onBeforeUnmount, ref, toRef, watch } from 'vue'
import { DropdownMenuContent, DropdownMenuItem, DropdownMenuPortal, DropdownMenuRoot, DropdownMenuTrigger, TabsContent, TabsIndicator, TabsList, TabsRoot, TabsTrigger } from 'reka-ui'
import Chip from '../ui/Chip.vue'
import Btn from '../ui/Btn.vue'
import Icon from '../ui/Icon.vue'
import Tip from '../ui/Tip.vue'
import Skeleton from '../ui/Skeleton.vue'
import EmptyState from '../ui/EmptyState.vue'
import OverviewTab from './OverviewTab.vue'
import TimelineTab from './TimelineTab.vue'
import SignalsTab from './SignalsTab.vue'
import ActionsTab from './ActionsTab.vue'
import ChatTab from './ChatTab.vue'
import { useIncidentData, useIncidentMutations } from '@/composables/useIncident'
import { useMe } from '@/composables/useMe'
import { startIncidentStream } from '@/api/sse'
import { absTime, duration, relTime, sevKey } from '@/lib/format'
import { useNow } from '@/lib/now'
import { ApiError, errorMessage } from '@/api/client'

const props = withDefaults(defineProps<{ id: number; mode?: 'drawer' | 'page'; initialTab?: string }>(), { mode: 'drawer', initialTab: 'overview' })
const emit = defineEmits<{ close: []; expand: [] }>()

const id = toRef(props, 'id')
const data = useIncidentData(id)
const m = useIncidentMutations(id)
const { isOperator } = useMe()
const now = useNow()

const inc = computed(() => data.incident.data.value)
const tab = ref(props.initialTab)
watch(id, () => (tab.value = 'overview'))

// per-incident live stream for agent progress
let stop: (() => void) | null = null
watch(
  id,
  (v) => {
    stop?.()
    stop = v > 0 ? startIncidentStream(v) : null
  },
  { immediate: true },
)
onBeforeUnmount(() => stop?.())

const pendingActions = computed(() => (data.actions.data.value ?? []).filter((a) => a.status === 'proposed').length)
const firing = computed(() => (data.signals.data.value ?? []).filter((s) => s.status === 'firing').length)
const isClosed = computed(() => inc.value?.status === 'resolved' || inc.value?.status === 'closed')
const dur = computed(() => {
  const i = inc.value
  if (!i) return ''
  const end = i.resolved_at ? new Date(i.resolved_at).getTime() : now.value
  return duration((end - new Date(i.opened_at).getTime()) / 1000)
})
const notFound = computed(() => data.incident.error.value instanceof ApiError && data.incident.error.value.status === 404)

const muteOptions = ['30m', '1h', '4h', '24h']

function openChat() {
  tab.value = 'chat'
}
defineExpose({ openChat })
</script>

<template>
  <div class="iv" :class="`iv--${mode}`">
    <template v-if="data.incident.isPending.value">
      <div class="iv__loading"><Skeleton :lines="2" :height="18" /><Skeleton :lines="6" /></div>
    </template>
    <EmptyState v-else-if="notFound" icon="incidents" title="Incident not found" :text="`#${id} does not exist or was deleted by retention.`">
      <Btn @click="emit('close')">Close</Btn>
    </EmptyState>
    <EmptyState v-else-if="data.incident.error.value" icon="info" title="Could not load incident" :text="errorMessage(data.incident.error.value)">
      <Btn icon="refresh" @click="data.incident.refetch()">Retry</Btn>
    </EmptyState>
    <template v-else-if="inc">
      <header class="iv__head" :class="`iv__head--${sevKey(inc.severity)}`">
        <div class="iv__topline">
          <span class="iv__id mono">#{{ inc.id }}</span>
          <Chip kind="severity" :value="inc.severity || 'none'" />
          <Chip kind="status" :value="inc.status" />
          <Chip v-if="inc.verdict !== 'unknown'" kind="verdict" :value="inc.verdict" />
          <span v-if="inc.muted" class="iv__flag"><Icon name="mute" :size="13" />muted</span>
          <span v-if="inc.reopen_count" class="iv__flag mono" :title="`reopened ${inc.reopen_count}×`"><Icon name="refresh" :size="12" />{{ inc.reopen_count }}</span>
          <span class="iv__spacer" />
          <Tip v-if="mode === 'drawer'" text="Open full page">
            <Btn variant="ghost" size="sm" icon="expand" aria-label="Open full page" @click="emit('expand')" />
          </Tip>
          <Tip v-if="mode === 'drawer'" text="Close (Esc)">
            <Btn variant="ghost" size="sm" icon="x" aria-label="Close" @click="emit('close')" />
          </Tip>
        </div>
        <h2 class="iv__title">{{ inc.title }}</h2>
        <div class="iv__meta mono">
          <span><span class="faint">cluster</span> {{ inc.cluster || '—' }}</span>
          <span v-if="inc.namespace"><span class="faint">ns</span> {{ inc.namespace }}</span>
          <span :title="absTime(inc.opened_at)"><span class="faint">opened</span> {{ relTime(inc.opened_at, now) }}</span>
          <span><span class="faint">{{ isClosed ? 'lasted' : 'for' }}</span> {{ dur }}</span>
          <span v-if="inc.source"><span class="faint">source</span> {{ inc.source }}</span>
        </div>
        <div class="iv__actions">
          <Btn
            v-if="inc.status === 'open'"
            size="sm"
            icon="check"
            :disabled="!isOperator"
            :loading="m.ack.isPending.value"
            :title="isOperator ? 'Acknowledge' : 'Operator role required'"
            @click="m.ack.mutate(undefined)"
            >Acknowledge</Btn
          >
          <Btn v-if="!isClosed" size="sm" icon="check" :disabled="!isOperator" :loading="m.resolve.isPending.value" @click="m.resolve.mutate(undefined)">Resolve</Btn>
          <Btn v-else size="sm" icon="refresh" :disabled="!isOperator" :loading="m.reopen.isPending.value" @click="m.reopen.mutate(undefined)">Reopen</Btn>
          <Btn
            size="sm"
            icon="sparkle"
            :disabled="!isOperator || inc.investigation_state === 'running' || inc.investigation_state === 'pending'"
            :loading="m.reinvestigate.isPending.value"
            @click="m.reinvestigate.mutate(undefined)"
            >{{ inc.investigation_state === 'running' ? 'Investigating…' : 'Reinvestigate' }}</Btn
          >
          <DropdownMenuRoot>
            <DropdownMenuTrigger as-child>
              <Btn size="sm" icon="mute" :disabled="!isOperator" :loading="m.mute.isPending.value">Mute <Icon name="chevronDown" :size="12" /></Btn>
            </DropdownMenuTrigger>
            <DropdownMenuPortal>
              <DropdownMenuContent class="menu" align="start" :side-offset="4">
                <DropdownMenuItem v-for="d in muteOptions" :key="d" class="menu__item mono" @select="m.mute.mutate({ duration: d })">Mute for {{ d }}</DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenuPortal>
          </DropdownMenuRoot>
          <span v-if="!isOperator" class="iv__ro faint">read-only</span>
        </div>
      </header>

      <TabsRoot v-model="tab" class="iv__tabs">
        <TabsList class="tabs" aria-label="Incident sections">
          <TabsTrigger value="overview" class="tab">Overview</TabsTrigger>
          <TabsTrigger value="timeline" class="tab">Timeline<span class="tab__n mono">{{ data.timeline.data.value?.length ?? '' }}</span></TabsTrigger>
          <TabsTrigger value="signals" class="tab">Signals<span class="tab__n mono" :class="{ 'tab__n--hot': firing }">{{ firing }}/{{ data.signals.data.value?.length ?? inc.signal_count }}</span></TabsTrigger>
          <TabsTrigger value="actions" class="tab">Actions<span v-if="pendingActions" class="tab__n tab__n--warn mono">{{ pendingActions }}</span></TabsTrigger>
          <TabsTrigger value="chat" class="tab">Chat<span class="tab__n mono">{{ data.messages.data.value?.length || '' }}</span></TabsTrigger>
          <TabsIndicator class="tabs__ind" />
        </TabsList>
        <div class="iv__body">
          <TabsContent value="overview" class="tabpane"><OverviewTab :incident="inc" :data="data" @goto="(t: string) => (tab = t)" /></TabsContent>
          <TabsContent value="timeline" class="tabpane"><TimelineTab :events="data.timeline.data.value" :loading="data.timeline.isPending.value" /></TabsContent>
          <TabsContent value="signals" class="tabpane"><SignalsTab :signals="data.signals.data.value" :loading="data.signals.isPending.value" /></TabsContent>
          <TabsContent value="actions" class="tabpane"><ActionsTab :incident="inc" :actions="data.actions.data.value" :loading="data.actions.isPending.value" /></TabsContent>
          <TabsContent value="chat" class="tabpane tabpane--chat"><ChatTab :incident="inc" :messages="data.messages.data.value" :loading="data.messages.isPending.value" /></TabsContent>
        </div>
      </TabsRoot>
    </template>
  </div>
</template>

<style scoped>
.iv {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  background: var(--bg-elev);
}
.iv__loading {
  padding: 20px;
  display: flex;
  flex-direction: column;
  gap: 24px;
}
.iv__head {
  --sev: var(--sev-other);
  padding: 12px 18px 12px;
  border-bottom: 1px solid var(--border);
  background: linear-gradient(180deg, color-mix(in srgb, var(--sev) 7%, transparent), transparent 70%);
  border-top: 2px solid var(--sev);
}
.iv__head--critical {
  --sev: var(--sev-critical);
}
.iv__head--warning {
  --sev: var(--sev-warning);
}
.iv__head--info {
  --sev: var(--sev-info);
}
.iv__topline {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}
.iv__id {
  color: var(--text-dim);
  font-size: var(--fs-sm);
  margin-right: 2px;
}
.iv__flag {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: var(--fs-xs);
  color: var(--text-dim);
}
.iv__spacer {
  flex: 1;
}
.iv__title {
  font-size: var(--fs-xl);
  line-height: 1.3;
  margin: 8px 0 6px;
  overflow-wrap: anywhere;
}
.iv__meta {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 16px;
  font-size: var(--fs-sm);
  color: var(--text);
}
.iv__actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  margin-top: 12px;
}
.iv__ro {
  font-size: var(--fs-xs);
  margin-left: 4px;
}
.iv__tabs {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
}
.tabs {
  position: relative;
  display: flex;
  gap: 2px;
  padding: 0 12px;
  border-bottom: 1px solid var(--border);
  overflow-x: auto;
  flex: none;
}
.tab {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  border: 0;
  background: none;
  padding: 10px 8px;
  color: var(--text-dim);
  font-weight: 500;
  white-space: nowrap;
}
.tab:hover {
  color: var(--text-strong);
}
.tab[data-state='active'] {
  color: var(--text-strong);
}
.tab__n {
  font-size: 10px;
  color: var(--text-faint);
}
.tab__n--hot {
  color: var(--sev-critical);
}
.tab__n--warn {
  color: var(--text-inverse);
  background: var(--sev-warning);
  border-radius: 8px;
  padding: 0 5px;
  line-height: 15px;
  font-weight: 600;
}
.tabs__ind {
  position: absolute;
  left: 0;
  bottom: -1px;
  height: 2px;
  width: var(--reka-tabs-indicator-size);
  transform: translateX(var(--reka-tabs-indicator-position));
  background: var(--accent);
  border-radius: 2px;
  transition: transform 180ms var(--ease), width 180ms var(--ease);
}
.iv__body {
  flex: 1;
  min-height: 0;
  overflow: auto;
}
.tabpane {
  padding: 16px 18px 28px;
  outline: none;
}
.tabpane--chat {
  padding: 0;
  height: 100%;
}
.iv--page .iv__head {
  padding: 16px 24px;
}
.iv--page .tabpane {
  padding: 18px 24px 32px;
}
.iv--page .tabpane--chat {
  padding: 0;
}
</style>
