<script setup lang="ts">
import type { IncidentDetail, Investigation } from '@/api/types'
import InvestigationCard from './InvestigationCard.vue'
import LabelList from '../ui/LabelList.vue'
import Skeleton from '../ui/Skeleton.vue'
import Chip from '../ui/Chip.vue'
import { absTime } from '@/lib/format'
import { useMe } from '@/composables/useMe'

defineProps<{ incident: IncidentDetail; investigations?: Investigation[]; loading?: boolean }>()
const { traceURL } = useMe()
</script>

<template>
  <div class="dt">
    <section>
      <h3 class="section-title">Labels</h3>
      <LabelList :labels="incident.labels" />
    </section>

    <section>
      <h3 class="section-title">Incident</h3>
      <dl class="dl mono">
        <dt>id</dt>
        <dd>#{{ incident.id }}</dd>
        <dt>source</dt>
        <dd>{{ incident.source || '—' }}</dd>
        <dt>opened</dt>
        <dd>{{ absTime(incident.opened_at) }}</dd>
        <template v-if="incident.acknowledged_at"><dt>acknowledged</dt><dd>{{ absTime(incident.acknowledged_at) }}</dd></template>
        <template v-if="incident.resolved_at"><dt>resolved</dt><dd>{{ absTime(incident.resolved_at) }}</dd></template>
        <dt>reopened</dt>
        <dd>{{ incident.reopen_count }}×</dd>
        <dt>investigation</dt>
        <dd>{{ incident.investigation_state }}<template v-if="incident.investigation_note"> · {{ incident.investigation_note }}</template></dd>
        <template v-if="incident.trace_id">
          <dt>trace</dt>
          <dd>
            <a v-if="traceURL(incident.trace_id)" :href="traceURL(incident.trace_id)" target="_blank" rel="noopener">{{ incident.trace_id }}</a>
            <template v-else>{{ incident.trace_id }}</template>
          </dd>
        </template>
      </dl>
    </section>

    <section>
      <h3 class="section-title">Investigations <span class="faint">{{ investigations?.length ?? '' }}</span></h3>
      <Skeleton v-if="loading" :lines="3" />
      <p v-else-if="!investigations?.length" class="muted">No investigation yet.</p>
      <div v-else class="invs">
        <div v-for="inv in investigations" :key="inv.id" class="inv">
          <div class="inv__meta mono">
            <Chip kind="action" :value="inv.status" dense />
            <span>{{ absTime(inv.created_at) }}</span>
            <span v-if="inv.error" class="inv__err">{{ inv.error }}</span>
          </div>
          <InvestigationCard v-if="inv.status === 'succeeded' || inv.summary" :inv="inv" />
        </div>
      </div>
    </section>
  </div>
</template>

<style scoped>
.dt {
  display: flex;
  flex-direction: column;
  gap: 22px;
}
.section-title {
  margin: 0 0 8px;
}
.dl {
  display: grid;
  grid-template-columns: 120px 1fr;
  gap: 5px 10px;
  margin: 0;
  font-size: var(--fs-sm);
}
.dl dt {
  color: var(--text-faint);
}
.dl dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.invs {
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.inv__meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  font-size: var(--fs-xs);
  color: var(--text-dim);
  margin-bottom: 6px;
}
.inv__err {
  color: var(--sev-warning);
}
</style>
