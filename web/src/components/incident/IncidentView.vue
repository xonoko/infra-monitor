<script setup lang="ts">
import { computed, onBeforeUnmount, ref, toRef, watch } from 'vue'
import { DropdownMenuContent, DropdownMenuItem, DropdownMenuPortal, DropdownMenuRoot, DropdownMenuTrigger } from 'reka-ui'
import Chip from '../ui/Chip.vue'
import Btn from '../ui/Btn.vue'
import Icon from '../ui/Icon.vue'
import Tip from '../ui/Tip.vue'
import Modal from '../ui/Modal.vue'
import Skeleton from '../ui/Skeleton.vue'
import EmptyState from '../ui/EmptyState.vue'
import IncidentStream from './IncidentStream.vue'
import IncidentGlance from './IncidentGlance.vue'
import SignalsTab from './SignalsTab.vue'
import DetailsTab from './DetailsTab.vue'
import ProposeActionForm from './ProposeActionForm.vue'
import ApproveDialog from './ApproveDialog.vue'
import { useActionTypes, useIncidentData, useIncidentMutations } from '@/composables/useIncident'
import { useMe } from '@/composables/useMe'
import { startIncidentStream } from '@/api/sse'
import { absTime, duration, relTime, sevKey } from '@/lib/format'
import { useNow } from '@/lib/now'
import { streamFilter } from '@/lib/streamFilter'
import { ApiError, errorMessage } from '@/api/client'

type View = 'activity' | 'signals' | 'details'
const props = withDefaults(defineProps<{ id: number; mode?: 'drawer' | 'page'; initialTab?: string }>(), { mode: 'drawer', initialTab: 'activity' })
const emit = defineEmits<{ close: []; expand: [] }>()

const id = toRef(props, 'id')
const data = useIncidentData(id)
const m = useIncidentMutations(id)
const { isOperator } = useMe()
const now = useNow()
const actionTypes = useActionTypes()

const inc = computed(() => data.incident.data.value)
const asView = (v: string): View => (v === 'signals' || v === 'details' ? v : 'activity')
const view = ref<View>(asView(props.initialTab))
watch(id, () => (view.value = 'activity'))

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

const firing = computed(() => (data.signals.data.value ?? []).filter((s) => s.status === 'firing').length)
const isClosed = computed(() => inc.value?.status === 'resolved' || inc.value?.status === 'closed')
const investigating = computed(() => inc.value?.investigation_state === 'running' || inc.value?.investigation_state === 'pending')
const dur = computed(() => {
  const i = inc.value
  if (!i) return ''
  const end = i.resolved_at ? new Date(i.resolved_at).getTime() : now.value
  return duration((end - new Date(i.opened_at).getTime()) / 1000)
})
const notFound = computed(() => data.incident.error.value instanceof ApiError && data.incident.error.value.status === 404)
const muteOptions = ['30m', '1h', '4h', '24h']

const proposing = ref(false)
const proposeDefaults = computed(() => ({ namespace: inc.value?.namespace ?? '', cluster: inc.value?.cluster ?? '' }))
function propose(v: { type: string; params: Record<string, unknown>; rationale?: string }) {
  m.propose.mutate(v, { onSuccess: () => (proposing.value = false) })
}

const stream = ref<InstanceType<typeof IncidentStream>>()
function openChat() {
  view.value = 'activity'
  stream.value?.focusComposer()
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
          <span v-if="inc.is_storm" class="iv__flag iv__flag--storm"><Icon name="incidents" :size="12" />storm</span>
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
          <span class="split">
            <Btn size="sm" icon="sparkle" :disabled="!isOperator || investigating" :loading="m.reinvestigate.isPending.value" @click="m.reinvestigate.mutate(false)">{{
              investigating ? 'Investigating…' : 'Reinvestigate'
            }}</Btn>
            <DropdownMenuRoot>
              <DropdownMenuTrigger as-child>
                <Btn size="sm" icon="chevronDown" :disabled="!isOperator || investigating" title="More investigation options" aria-label="More investigation options" />
              </DropdownMenuTrigger>
              <DropdownMenuPortal>
                <DropdownMenuContent class="menu" align="start" :side-offset="4">
                  <DropdownMenuItem class="menu__item" @select="m.reinvestigate.mutate(true)"><Icon name="sparkle" />Dig deeper (bigger model, costs more)</DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenuPortal>
            </DropdownMenuRoot>
          </span>
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

      <div class="iv__main">
        <div class="iv__grid">
          <div class="iv__glance">
            <IncidentGlance
              :incident="inc"
              :actions="data.actions.data.value"
              :signals="data.signals.data.value"
              :timeline="data.timeline.data.value"
              @goto="(v: string) => (view = asView(v))"
              @propose="proposing = true"
            />
          </div>
          <section class="iv__content">
            <nav class="vstrip" aria-label="Incident views">
              <div class="vstrip__views" role="tablist">
                <button role="tab" class="vtab" :aria-selected="view === 'activity'" :class="{ on: view === 'activity' }" @click="view = 'activity'">
                  <Icon name="message" :size="13" />Activity
                </button>
                <button role="tab" class="vtab" :aria-selected="view === 'signals'" :class="{ on: view === 'signals' }" @click="view = 'signals'">
                  Signals <span class="mono vtab__n" :class="{ hot: firing }">{{ firing }}/{{ data.signals.data.value?.length ?? inc.signal_count }}</span>
                </button>
                <button role="tab" class="vtab" :aria-selected="view === 'details'" :class="{ on: view === 'details' }" @click="view = 'details'">Details</button>
              </div>
              <div v-if="view === 'activity'" class="seg" role="radiogroup" aria-label="Show">
                <button role="radio" :aria-checked="streamFilter === 'all'" :class="{ on: streamFilter === 'all' }" @click="streamFilter = 'all'">Everything</button>
                <button role="radio" :aria-checked="streamFilter === 'conversation'" :class="{ on: streamFilter === 'conversation' }" @click="streamFilter = 'conversation'">
                  Conversation
                </button>
              </div>
            </nav>
            <div class="iv__view">
              <IncidentStream
                v-if="view === 'activity'"
                ref="stream"
                :incident="inc"
                :timeline="data.timeline.data.value"
                :messages="data.messages.data.value"
                :actions="data.actions.data.value"
                :investigations="data.investigations.data.value"
                :loading="data.timeline.isPending.value || data.messages.isPending.value"
              />
              <div v-else class="iv__scroll">
                <SignalsTab v-if="view === 'signals'" :signals="data.signals.data.value" :loading="data.signals.isPending.value" />
                <DetailsTab v-else :incident="inc" :investigations="data.investigations.data.value" :loading="data.investigations.isPending.value" />
              </div>
            </div>
          </section>
        </div>
      </div>

      <Modal v-model:open="proposing" title="Propose an action" description="It still needs approval by an operator before it runs." :width="520">
        <ProposeActionForm
          v-if="actionTypes.data.value?.length"
          :types="actionTypes.data.value"
          :defaults="proposeDefaults"
          :busy="m.propose.isPending.value"
          @submit="propose"
          @cancel="proposing = false"
        />
        <p v-else class="muted">No action type is enabled on this server.</p>
      </Modal>
      <ApproveDialog />
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
  padding: 10px 16px;
  border-bottom: 1px solid var(--border);
  background: linear-gradient(180deg, color-mix(in srgb, var(--sev) 7%, transparent), transparent 70%);
  border-top: 2px solid var(--sev);
  flex: none;
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
.iv__flag--storm {
  color: var(--sev-info);
}
.iv__spacer {
  flex: 1;
}
.iv__title {
  font-size: var(--fs-xl);
  line-height: 1.3;
  margin: 6px 0 4px;
  overflow-wrap: anywhere;
}
.iv__meta {
  display: flex;
  flex-wrap: wrap;
  gap: 2px 16px;
  font-size: var(--fs-sm);
  color: var(--text);
}
.iv__actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  margin-top: 10px;
}
.split {
  display: inline-flex;
}
.split > :first-child {
  border-top-right-radius: 0;
  border-bottom-right-radius: 0;
}
.split > :last-child {
  margin-left: -1px;
  border-top-left-radius: 0;
  border-bottom-left-radius: 0;
}
.iv__ro {
  font-size: var(--fs-xs);
  margin-left: 4px;
}

/* body: glance panel + views. Layout depends on the width of the incident view itself. */
.iv__main {
  flex: 1;
  min-height: 0;
  container: incident / inline-size;
}
.iv__grid {
  height: 100%;
  display: flex;
  flex-direction: column;
}
.iv__glance {
  flex: none;
  max-height: 45%;
  overflow-y: auto;
  border-bottom: 1px solid var(--border);
  background: var(--bg-elev);
}
.iv__content {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  background: var(--bg);
}
@container incident (min-width: 900px) {
  .iv__grid {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 310px;
  }
  .iv__glance {
    order: 2;
    max-height: none;
    height: 100%;
    border-bottom: 0;
    border-left: 1px solid var(--border);
  }
  .iv__content {
    order: 1;
    height: 100%;
  }
}

.vstrip {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 0 12px;
  height: 36px;
  border-bottom: 1px solid var(--border);
  background: var(--bg-elev);
  flex: none;
}
.vstrip__views {
  display: flex;
  gap: 2px;
  height: 100%;
}
.vtab {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  border: 0;
  background: none;
  padding: 0 8px;
  color: var(--text-dim);
  font-weight: 500;
  font-size: var(--fs-sm);
  border-bottom: 2px solid transparent;
  margin-bottom: -1px;
  white-space: nowrap;
}
.vtab:hover {
  color: var(--text-strong);
}
.vtab.on {
  color: var(--text-strong);
  border-bottom-color: var(--accent);
}
.vtab__n {
  font-size: 10px;
  color: var(--text-faint);
}
.hot {
  color: var(--sev-critical);
}
.seg {
  display: inline-flex;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius);
  overflow: hidden;
  flex: none;
}
.seg button {
  border: 0;
  background: transparent;
  color: var(--text-dim);
  padding: 0 9px;
  height: 22px;
  font-size: var(--fs-xs);
}
.seg button.on {
  background: var(--bg-active);
  color: var(--text-strong);
}
.iv__view {
  flex: 1;
  min-height: 0;
}
.iv__scroll {
  height: 100%;
  overflow: auto;
  padding: 14px 16px 24px;
}
.iv--page .iv__head {
  padding: 14px 24px;
}
@media (max-width: 420px) {
  .seg button {
    padding: 0 6px;
  }
}
</style>
