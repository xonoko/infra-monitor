<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import type { IncidentDetail, Message } from '@/api/types'
import Btn from '../ui/Btn.vue'
import Icon from '../ui/Icon.vue'
import Markdown from '../ui/Markdown.vue'
import Skeleton from '../ui/Skeleton.vue'
import { useIncidentMutations } from '@/composables/useIncident'
import { useMe } from '@/composables/useMe'
import { markThinking, progressFor } from '@/lib/progress'
import { absTime, clockTime } from '@/lib/format'

const props = defineProps<{ incident: IncidentDetail; messages?: Message[]; loading?: boolean }>()
const id = computed(() => props.incident.id)
const m = useIncidentMutations(id)
const { isOperator, me } = useMe()
const progress = computed(() => progressFor(props.incident.id))

const draft = ref('')
const scroller = ref<HTMLElement>()
const pendingAgent = computed(() => (props.messages ?? []).some((x) => x.role === 'agent' && x.status === 'pending'))
const working = computed(() => progress.value.active || pendingAgent.value)

function scrollDown() {
  void nextTick(() => {
    const el = scroller.value
    if (el) el.scrollTop = el.scrollHeight
  })
}
watch(() => [props.messages?.length, progress.value.items.length, working.value], scrollDown, { immediate: true })

function send() {
  const content = draft.value.trim()
  if (!content || m.send.isPending.value) return
  m.send.mutate(content, {
    onSuccess: () => {
      draft.value = ''
      markThinking(props.incident.id)
    },
  })
}
function onKey(e: KeyboardEvent) {
  if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
    e.preventDefault()
    send()
  }
}

function who(msg: Message) {
  if (msg.role === 'agent') return msg.author_name || 'Agent'
  if (msg.role === 'system') return 'system'
  return msg.author_name || 'someone'
}
const isMine = (msg: Message) => msg.role === 'user' && !!me.value && (msg.author_name === me.value.display_name || msg.author_name === me.value.email)

const suggestions = ['What changed right before this started?', 'Show the logs of the failing pods', 'Is this related to other open incidents?']
</script>

<template>
  <div class="chat">
    <div ref="scroller" class="chat__log">
      <Skeleton v-if="loading" :lines="5" />
      <div v-else-if="!messages?.length && !working" class="chat__empty">
        <div class="chat__emptyico"><Icon name="sparkle" :size="18" /></div>
        <p>Ask the agent about this incident. Replies to the incident card in Telegram land here too.</p>
        <div v-if="isOperator" class="chat__sugg">
          <button v-for="s in suggestions" :key="s" class="chat__suggbtn" @click="draft = s">{{ s }}</button>
        </div>
      </div>
      <template v-else>
        <article v-for="msg in messages" :key="msg.id" class="msg" :class="[`msg--${msg.role}`, { 'msg--mine': isMine(msg), 'msg--failed': msg.status === 'failed' }]">
          <header class="msg__head">
            <span class="msg__who">
              <Icon v-if="msg.role === 'agent'" name="sparkle" :size="12" />
              {{ who(msg) }}
            </span>
            <span v-if="msg.role !== 'agent' && msg.channel && msg.channel !== 'ui'" class="msg__via mono">
              <Icon v-if="msg.channel === 'telegram'" name="telegram" :size="11" /> via {{ msg.channel }}
            </span>
            <time class="msg__time mono" :title="absTime(msg.created_at)">{{ clockTime(msg.created_at) }}</time>
            <span v-if="msg.status === 'pending'" class="msg__state mono">sending…</span>
            <span v-if="msg.status === 'failed'" class="msg__state msg__state--err mono">failed</span>
          </header>
          <div class="msg__body"><Markdown :source="msg.content" /></div>
          <div v-if="msg.action_ids?.length" class="msg__acts mono"><Icon name="bolt" :size="11" /> proposed {{ msg.action_ids.length }} action{{ msg.action_ids.length > 1 ? 's' : '' }} — see Actions</div>
        </article>
      </template>

      <div v-if="working" class="live" role="status" aria-live="polite">
        <div class="live__head"><span class="dots" aria-hidden="true"><i /><i /><i /></span> Agent is working</div>
        <ol v-if="progress.items.length" class="live__list mono">
          <li v-for="(p, i) in progress.items" :key="i" :class="`live__item--${p.kind}`">
            <Icon :name="p.kind === 'tool_call' ? 'terminal' : p.kind === 'tool_result' ? 'check' : 'message'" :size="11" />
            <span v-if="p.tool" class="live__tool">{{ p.tool }}</span>
            <span v-if="p.text" class="live__text">{{ p.text }}</span>
          </li>
        </ol>
      </div>
    </div>

    <form class="composer" @submit.prevent="send">
      <textarea
        v-model="draft"
        class="composer__input"
        rows="2"
        :placeholder="isOperator ? 'Ask the agent… (Enter to send, Shift+Enter for a new line)' : 'Operator role required to chat'"
        :disabled="!isOperator"
        aria-label="Message the agent"
        @keydown="onKey"
      />
      <Btn variant="primary" icon="send" type="submit" :disabled="!isOperator || !draft.trim()" :loading="m.send.isPending.value" aria-label="Send">Send</Btn>
    </form>
  </div>
</template>

<style scoped>
.chat {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 360px;
}
.chat__log {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 16px 18px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.chat__empty {
  margin: auto;
  max-width: 420px;
  text-align: center;
  color: var(--text-dim);
}
.chat__emptyico {
  width: 38px;
  height: 38px;
  border-radius: 10px;
  margin: 0 auto 10px;
  display: grid;
  place-items: center;
  color: var(--accent);
  background: var(--accent-soft);
}
.chat__sugg {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-top: 14px;
}
.chat__suggbtn {
  border: 1px solid var(--border-strong);
  background: var(--bg-panel);
  color: var(--text);
  border-radius: var(--radius);
  padding: 7px 10px;
  text-align: left;
}
.chat__suggbtn:hover {
  border-color: var(--accent);
  color: var(--text-strong);
}
.msg {
  max-width: 92%;
  border-radius: var(--radius-lg);
  padding: 8px 12px 10px;
  border: 1px solid var(--border);
  background: var(--bg-panel);
}
.msg--agent {
  border-color: color-mix(in srgb, var(--accent) 25%, var(--border));
  background: linear-gradient(180deg, color-mix(in srgb, var(--accent) 6%, var(--bg-panel)), var(--bg-panel));
}
.msg--mine {
  align-self: flex-end;
  background: var(--bg-active);
}
.msg--system {
  align-self: center;
  background: transparent;
  border-style: dashed;
  color: var(--text-dim);
  font-size: var(--fs-sm);
}
.msg--failed {
  border-color: var(--danger);
}
.msg__head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 4px;
  font-size: var(--fs-xs);
}
.msg__who {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-weight: 600;
  color: var(--text-strong);
  font-size: var(--fs-sm);
}
.msg--agent .msg__who {
  color: var(--accent);
}
.msg__via {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  color: var(--text-dim);
}
.msg__time {
  color: var(--text-faint);
  margin-left: auto;
}
.msg__state {
  color: var(--text-faint);
}
.msg__state--err {
  color: var(--danger);
}
.msg__acts {
  display: flex;
  align-items: center;
  gap: 4px;
  margin-top: 6px;
  font-size: var(--fs-xs);
  color: var(--sev-warning);
}
.live {
  border: 1px dashed color-mix(in srgb, var(--accent) 40%, var(--border));
  border-radius: var(--radius-lg);
  padding: 8px 12px;
  background: color-mix(in srgb, var(--accent) 4%, transparent);
}
.live__head {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--accent);
  font-weight: 500;
  font-size: var(--fs-sm);
}
.live__list {
  list-style: none;
  margin: 8px 0 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 3px;
  font-size: var(--fs-xs);
  color: var(--text-dim);
  max-height: 180px;
  overflow: auto;
}
.live__list li {
  display: flex;
  gap: 6px;
  align-items: baseline;
  min-width: 0;
}
.live__tool {
  color: var(--text-strong);
  white-space: nowrap;
}
.live__text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.dots {
  display: inline-flex;
  gap: 3px;
}
.dots i {
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: var(--accent);
  animation: blink 1.2s infinite ease-in-out;
}
.dots i:nth-child(2) {
  animation-delay: 0.15s;
}
.dots i:nth-child(3) {
  animation-delay: 0.3s;
}
@keyframes blink {
  0%,
  80%,
  100% {
    opacity: 0.2;
  }
  40% {
    opacity: 1;
  }
}
.composer {
  display: flex;
  gap: 8px;
  align-items: flex-end;
  padding: 10px 14px;
  border-top: 1px solid var(--border);
  background: var(--bg-elev);
}
.composer__input {
  flex: 1;
  resize: none;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius);
  background: var(--bg-input);
  padding: 7px 10px;
  color: var(--text-strong);
  outline: none;
  line-height: 1.45;
  max-height: 160px;
}
.composer__input:focus {
  border-color: var(--border-focus);
  box-shadow: 0 0 0 3px var(--accent-ring);
}
.composer__input:disabled {
  opacity: 0.6;
}
</style>
