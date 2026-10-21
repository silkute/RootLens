<script setup lang="ts">
import { ref, watch } from "vue";
import { api, number, relative } from "../api";
import type { Process } from "../contracts.generated";
import Icon from "./Icon.vue";
const props = defineProps<{ hostId: string; updated?: string | null }>();
const expanded = ref(false);
const snapshot = ref<{
  status: string;
  reason: string;
  at?: string;
  visible?: number;
  limited?: boolean;
  processes: Process[];
} | null>(null);
const error = ref("");
async function load() {
  if (!expanded.value) return;
  try {
    snapshot.value = await api(`/hosts/${props.hostId}/processes`);
    error.value = "";
  } catch (e) {
    error.value = (e as Error).message;
  }
}
watch(() => [expanded.value, props.hostId, props.updated], load);
</script>
<template>
  <section class="card process-card">
    <button
      class="process-toggle"
      @click="expanded = !expanded"
      :aria-expanded="expanded"
    >
      <span><Icon name="cpu" :size="16" />Current process leaders</span
      ><Icon name="down" :size="14" /></button
    ><template v-if="expanded"
      ><p v-if="error" class="notice">{{ error }}</p>
      <template v-if="snapshot"
        ><div class="notice">
          <Icon name="info" :size="16" />
          <p>
            {{ snapshot.reason
            }}<span v-if="snapshot.at">
              Updated {{ relative(snapshot.at) }}.</span
            ><span v-if="snapshot.limited"> Visibility is incomplete.</span
            ><span v-if="snapshot.status === 'stale'">
              These measurements are stale.</span
            >
          </p>
        </div>
        <div v-if="snapshot.processes.length" class="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Process</th>
                <th>PID</th>
                <th>CPU · one core</th>
                <th>Resident memory</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="process in snapshot.processes" :key="process.pid">
                <td>{{ process.name }}</td>
                <td>{{ process.pid }}</td>
                <td>{{ number(process.cpu_percent ?? undefined, "%") }}</td>
                <td>{{ number(process.rss_bytes, "bytes") }}</td>
              </tr>
            </tbody>
          </table>
        </div></template
      >
      <p v-else class="muted small">
        Loading current process visibility…
      </p></template
    >
  </section>
</template>
