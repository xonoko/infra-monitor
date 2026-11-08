<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { ActionType, JSONSchema } from '@/api/types'
import Btn from '../ui/Btn.vue'
import Chip from '../ui/Chip.vue'

const props = defineProps<{ types: ActionType[]; defaults: Record<string, string>; busy?: boolean }>()
const emit = defineEmits<{ submit: [v: { type: string; params: Record<string, unknown>; rationale?: string }]; cancel: [] }>()

const type = ref(props.types[0]?.type ?? '')
const current = computed(() => props.types.find((t) => t.type === type.value))
const schema = computed<JSONSchema>(() => current.value?.params_schema ?? {})

type FieldKind = 'string' | 'integer' | 'number' | 'boolean' | 'enum'
interface F {
  name: string
  kind: FieldKind
  required: boolean
  s: JSONSchema
}

// Only flat object schemas with scalar properties get a generated form.
const fields = computed<F[] | null>(() => {
  const s = schema.value
  if (s.type !== 'object' || !s.properties) return Object.keys(s).length ? null : []
  const out: F[] = []
  for (const [name, p] of Object.entries(s.properties)) {
    let kind: FieldKind
    if (Array.isArray(p.enum)) kind = 'enum'
    else if (p.type === 'string' || p.type === 'integer' || p.type === 'number' || p.type === 'boolean') kind = p.type
    else return null
    out.push({ name, kind, required: !!s.required?.includes(name), s: p })
  }
  return out
})

const values = ref<Record<string, unknown>>({})
const json = ref('{}')
const rationale = ref('')
const error = ref('')

watch(
  [type, fields],
  () => {
    const v: Record<string, unknown> = {}
    for (const f of fields.value ?? []) {
      if (f.s.default !== undefined) v[f.name] = f.s.default
      else if (props.defaults[f.name] !== undefined && f.kind === 'string') v[f.name] = props.defaults[f.name]
      else if (f.kind === 'boolean') v[f.name] = false
    }
    values.value = v
    json.value = JSON.stringify(
      Object.fromEntries(Object.keys(schema.value.properties ?? {}).map((k) => [k, props.defaults[k] ?? ''])),
      null,
      2,
    )
    error.value = ''
  },
  { immediate: true },
)

function submit() {
  error.value = ''
  let params: Record<string, unknown>
  if (fields.value) {
    params = {}
    for (const f of fields.value) {
      const raw = values.value[f.name]
      if (raw === '' || raw === undefined || raw === null) {
        if (f.required) {
          error.value = `${f.name} is required`
          return
        }
        continue
      }
      params[f.name] = f.kind === 'integer' ? parseInt(String(raw), 10) : f.kind === 'number' ? Number(raw) : raw
    }
  } else {
    try {
      params = JSON.parse(json.value)
    } catch {
      error.value = 'Params must be valid JSON'
      return
    }
  }
  emit('submit', { type: type.value, params, rationale: rationale.value || undefined })
}
</script>

<template>
  <form class="pf" @submit.prevent="submit">
    <div class="pf__row">
      <label class="label" for="pf-type">Action</label>
      <select id="pf-type" v-model="type" class="select mono">
        <option v-for="t in types" :key="t.type" :value="t.type">{{ t.type }}</option>
      </select>
      <p v-if="current" class="pf__desc">
        {{ current.description }} <Chip kind="risk" :value="`${current.risk} risk`" dense />
        <span v-if="current.auto_approve" class="faint"> · auto-approved</span>
      </p>
    </div>
    <template v-if="fields">
      <div v-for="f in fields" :key="f.name" class="pf__row">
        <label class="label" :for="`pf-${f.name}`">{{ f.s.title ?? f.name }}<span v-if="f.required" class="req">*</span></label>
        <select v-if="f.kind === 'enum'" :id="`pf-${f.name}`" v-model="values[f.name]" class="select mono">
          <option v-for="o in f.s.enum" :key="String(o)" :value="o">{{ o }}</option>
        </select>
        <label v-else-if="f.kind === 'boolean'" class="pf__check"><input :id="`pf-${f.name}`" v-model="values[f.name]" type="checkbox" /> {{ f.s.description ?? f.name }}</label>
        <input
          v-else
          :id="`pf-${f.name}`"
          v-model="values[f.name]"
          class="input mono"
          :type="f.kind === 'string' ? 'text' : 'number'"
          :min="f.s.minimum"
          :max="f.s.maximum"
          :placeholder="f.s.description"
        />
        <p v-if="f.s.description && f.kind !== 'boolean'" class="pf__hint">{{ f.s.description }}</p>
      </div>
    </template>
    <div v-else class="pf__row">
      <label class="label" for="pf-json">Params (JSON)</label>
      <textarea id="pf-json" v-model="json" class="textarea mono" rows="6" spellcheck="false" />
    </div>
    <div class="pf__row">
      <label class="label" for="pf-why">Rationale</label>
      <textarea id="pf-why" v-model="rationale" class="textarea" rows="2" placeholder="Why should this run?" />
    </div>
    <p v-if="error" class="pf__err">{{ error }}</p>
    <div class="pf__foot">
      <Btn variant="ghost" @click="emit('cancel')">Cancel</Btn>
      <Btn variant="primary" type="submit" :loading="busy">Propose</Btn>
    </div>
  </form>
</template>

<style scoped>
.pf {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.pf__desc,
.pf__hint {
  margin: 6px 0 0;
  font-size: var(--fs-sm);
  color: var(--text-dim);
}
.pf__check {
  display: flex;
  gap: 8px;
  align-items: center;
}
.req {
  color: var(--danger);
  margin-left: 2px;
}
.pf__err {
  color: var(--danger);
  margin: 0;
}
.pf__foot {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
</style>
