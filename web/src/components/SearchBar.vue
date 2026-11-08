<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import Icon from './ui/Icon.vue'
import Kbd from './ui/Kbd.vue'
import { SEARCH_KEYS, tokenize, type SearchKey } from '@/lib/search'

const model = defineModel<string>({ required: true })
const props = defineProps<{ suggestions?: Partial<Record<SearchKey, string[]>> }>()
const emit = defineEmits<{ submit: [] }>()

const draft = ref(model.value)
watch(model, (v) => (draft.value = v))

const input = ref<HTMLInputElement>()
const focused = ref(false)
const active = ref(0)

defineExpose({ focus: () => input.value?.focus() })

const lastWord = computed(() => {
  const m = /(\S*)$/.exec(draft.value)
  return m ? m[1] : ''
})

interface Sugg {
  label: string
  insert: string
  hint?: string
}

const items = computed<Sugg[]>(() => {
  const w = lastWord.value
  const i = w.indexOf(':')
  if (i > 0) {
    const key = w.slice(0, i).toLowerCase() as SearchKey
    if (!(key in SEARCH_KEYS)) return []
    const typed = w.slice(i + 1)
    const prefix = typed.includes(',') ? typed.slice(0, typed.lastIndexOf(',') + 1) : ''
    const cur = typed.slice(prefix.length).toLowerCase()
    const pool = [...new Set([...(SEARCH_KEYS[key].values as readonly string[]), ...(props.suggestions?.[key] ?? [])])]
    return pool
      .filter((v) => v.toLowerCase().startsWith(cur) && v.toLowerCase() !== cur)
      .slice(0, 8)
      .map((v) => ({ label: `${key}:${prefix}${v}`, insert: `${key}:${prefix}${v}` }))
  }
  if (!w) return []
  return (Object.keys(SEARCH_KEYS) as SearchKey[])
    .filter((k) => k.startsWith(w.toLowerCase()))
    .map((k) => ({ label: `${k}:`, insert: `${k}:`, hint: SEARCH_KEYS[k].list ? 'a,b' : 'value' }))
})

const open = computed(() => focused.value && items.value.length > 0)
watch(items, () => (active.value = 0))

function apply(s: Sugg) {
  draft.value = draft.value.slice(0, draft.value.length - lastWord.value.length) + s.insert
  if (!s.insert.endsWith(':')) {
    draft.value += ' '
    commit()
  }
  void nextTick(() => input.value?.focus())
}

function commit() {
  model.value = draft.value.trim().replace(/\s+/g, ' ')
  emit('submit')
}

function onKey(e: KeyboardEvent) {
  if (open.value && (e.key === 'ArrowDown' || e.key === 'ArrowUp')) {
    e.preventDefault()
    const n = items.value.length
    active.value = (active.value + (e.key === 'ArrowDown' ? 1 : n - 1)) % n
    return
  }
  if (e.key === 'Tab' && open.value) {
    e.preventDefault()
    apply(items.value[active.value])
    return
  }
  if (e.key === 'Enter') {
    e.preventDefault()
    if (open.value && lastWord.value && !lastWord.value.endsWith(' ')) {
      const s = items.value[active.value]
      if (s && s.insert !== lastWord.value) return apply(s)
    }
    commit()
    input.value?.blur()
  }
  if (e.key === 'Escape') {
    draft.value = model.value
    input.value?.blur()
  }
}

const tokens = computed(() => tokenize(draft.value))
</script>

<template>
  <div class="search" :class="{ 'search--focus': focused }">
    <Icon name="search" class="search__icon" />
    <div class="search__field">
      <!-- token highlight layer mirrors the input text -->
      <div class="search__mirror mono" aria-hidden="true">
        <template v-for="(t, i) in tokens" :key="i">
          <span v-if="t.key" class="tok"><span class="tok__k">{{ t.raw.slice(0, t.raw.indexOf(':') + 1) }}</span><span class="tok__v">{{ t.raw.slice(t.raw.indexOf(':') + 1) }}</span></span><span v-else>{{ t.raw }}</span>{{ ' ' }}
        </template>
      </div>
      <input
        ref="input"
        v-model="draft"
        class="search__input mono"
        type="text"
        spellcheck="false"
        autocomplete="off"
        placeholder="Search incidents…  severity:critical cluster:manager status:open"
        aria-label="Search incidents"
        role="combobox"
        :aria-expanded="open"
        aria-controls="search-suggestions"
        @focus="focused = true"
        @blur="focused = false; draft !== model && commit()"
        @keydown="onKey"
      />
    </div>
    <button v-if="draft" class="search__clear" aria-label="Clear search" @mousedown.prevent="draft = ''; commit()">
      <Icon name="x" :size="14" />
    </button>
    <Kbd v-else class="search__kbd">/</Kbd>
    <ul v-if="open" id="search-suggestions" class="sugg" role="listbox">
      <li
        v-for="(s, i) in items"
        :key="s.label"
        role="option"
        :aria-selected="i === active"
        class="sugg__item mono"
        :class="{ 'sugg__item--active': i === active }"
        @mousedown.prevent="apply(s)"
        @mouseenter="active = i"
      >
        {{ s.label }}<span v-if="s.hint" class="sugg__hint">{{ s.hint }}</span>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.search {
  position: relative;
  display: flex;
  align-items: center;
  gap: 8px;
  height: 32px;
  padding: 0 10px;
  background: var(--bg-input);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius);
  flex: 1;
  min-width: 0;
  transition: border-color var(--dur) var(--ease), box-shadow var(--dur) var(--ease);
}
.search--focus {
  border-color: var(--border-focus);
  box-shadow: 0 0 0 3px var(--accent-ring);
}
.search__icon {
  color: var(--text-faint);
}
.search__field {
  position: relative;
  flex: 1;
  min-width: 0;
  height: 100%;
}
.search__mirror,
.search__input {
  position: absolute;
  inset: 0;
  font-size: var(--fs-sm);
  line-height: 30px;
  white-space: pre;
  letter-spacing: 0;
  padding: 0;
}
.search__mirror {
  color: var(--text-strong);
  overflow: hidden;
  pointer-events: none;
}
.search__input {
  width: 100%;
  border: 0;
  outline: none;
  background: transparent;
  color: transparent;
  caret-color: var(--text-strong);
}
.search__input::placeholder {
  color: var(--text-faint);
}
.search--focus .search__mirror {
  visibility: hidden;
}
.search--focus .search__input {
  color: var(--text-strong);
}
.tok__k {
  color: var(--accent);
}
.tok__v {
  color: var(--text-strong);
  background: var(--accent-soft);
  border-radius: 2px;
}
.search__clear {
  border: 0;
  background: none;
  color: var(--text-faint);
  padding: 2px;
  display: grid;
}
.search__clear:hover {
  color: var(--text-strong);
}
.sugg {
  position: absolute;
  top: calc(100% + 6px);
  left: 0;
  min-width: 280px;
  list-style: none;
  margin: 0;
  padding: 4px;
  background: var(--bg-elev);
  border-radius: var(--radius);
  box-shadow: var(--shadow-pop);
  z-index: 40;
}
.sugg__item {
  display: flex;
  justify-content: space-between;
  padding: 5px 8px;
  border-radius: 4px;
  font-size: var(--fs-sm);
  cursor: pointer;
}
.sugg__item--active {
  background: var(--bg-hover);
  color: var(--text-strong);
}
.sugg__hint {
  color: var(--text-faint);
  margin-left: 16px;
}
@media (max-width: 640px) {
  .search__kbd {
    display: none;
  }
}
</style>
