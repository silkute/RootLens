<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { api } from "../api";
import type { Host, MetricsResponse } from "../types";
import MetricChart from "./MetricChart.vue";
import Icon from "./Icon.vue";
const props = defineProps<{
  host: Host;
  metrics: MetricsResponse | null;
  theme: string;
}>();
const expanded = ref(false);
const core = ref("core:cpu0");
const coreMetrics = ref<MetricsResponse | null>(null);
const error = ref("");
const cores = computed(() =>
  Array.from(
    { length: Math.min(256, props.host.latest["cpu.count"] || 0) },
    (_, i) => i,
  ),
);
const series = (kind: string) =>
  props.metrics?.series.filter((s) => s.entity.kind === kind) || [];
const resolution = computed(() => props.metrics?.resolution_seconds || 5);
async function loadCore() {
  if (!expanded.value || !props.metrics) return;
  try {
    coreMetrics.value = await api(
      `/hosts/${props.host.id}/metrics?entity=${core.value}&from=${encodeURIComponent(props.metrics.from)}&to=${encodeURIComponent(props.metrics.to)}`,
    );
    error.value = "";
  } catch (e) {
    error.value = (e as Error).message;
  }
}
watch(() => [expanded.value, core.value, props.metrics?.to], loadCore);
</script>
<template>
  <div class="additional-metrics">
    <button
      class="button"
      :aria-expanded="expanded"
      @click="expanded = !expanded"
    >
      <Icon name="activity" :size="16" />{{
        expanded ? "Hide additional metrics" : "Additional resource metrics"
      }}<Icon name="down" :size="14" />
    </button>
    <template v-if="expanded"
      ><div class="section-heading">
        <h2>Per-core CPU</h2>
        <label class="sort-control"
          >Core<select v-model="core">
            <option v-for="id in cores" :key="id" :value="`core:cpu${id}`">
              CPU {{ id }}
            </option>
          </select></label
        >
      </div>
      <p v-if="error" class="notice">{{ error }}</p>
      <div class="chart-grid">
        <MetricChart
          title="Selected CPU core"
          subtitle="Execution on one logical processor"
          icon="cpu"
          metric="cpu.busy_pct"
          :series="coreMetrics?.series || []"
          unit="%"
          color="#6e9dec"
          :resolution="resolution"
          :theme="theme"
        /><MetricChart
          title="CPU pressure"
          subtitle="Time tasks waited for CPU scheduling"
          icon="cpu"
          metric="cpu.some_pct"
          :series="series('host')"
          unit="%"
          color="#6e9dec"
          :resolution="resolution"
          :theme="theme"
        /><MetricChart
          title="Memory pressure"
          subtitle="Time tasks stalled on memory"
          icon="memory"
          metric="memory.some_pct"
          :series="series('host')"
          unit="%"
          color="#56b5a9"
          :resolution="resolution"
          :theme="theme"
        /><MetricChart
          title="Swap activity"
          subtitle="Bytes swapped out per second"
          icon="memory"
          metric="swap.out_bytes"
          :series="series('host')"
          unit="rate"
          color="#56b5a9"
          :resolution="resolution"
          :theme="theme"
        /><MetricChart
          title="Disk writes"
          subtitle="Accounted block writes per second"
          icon="disk"
          metric="disk.write_bytes"
          :series="series('device')"
          unit="rate"
          color="#9c8bd9"
          :resolution="resolution"
          :theme="theme"
        /><MetricChart
          title="Read operations"
          subtitle="Completed operations per second"
          icon="disk"
          metric="disk.read_ops"
          :series="series('device')"
          unit=" IOPS"
          color="#dfaa63"
          :resolution="resolution"
          :theme="theme"
        /><MetricChart
          title="Outstanding I/O"
          subtitle="Time-weighted queue occupancy"
          icon="activity"
          metric="disk.queue"
          :series="series('device')"
          unit=""
          color="#dfaa63"
          :resolution="resolution"
          :theme="theme"
        /><MetricChart
          title="Device activity"
          subtitle="Busy-time estimate, not saturation"
          icon="disk"
          metric="disk.busy_pct"
          :series="series('device')"
          unit="%"
          color="#9c8bd9"
          :resolution="resolution"
          :theme="theme"
        /><MetricChart
          title="Network transmitted"
          subtitle="Host interface bytes sent per second"
          icon="network"
          metric="net.tx_bytes"
          :series="series('network')"
          unit="rate"
          color="#6e9dec"
          :resolution="resolution"
          :theme="theme"
        /><MetricChart
          title="Network errors"
          subtitle="Observed counter increments per second"
          icon="network"
          metric="net.errors"
          :series="series('network')"
          unit=" /s"
          color="#dfaa63"
          :resolution="resolution"
          :theme="theme"
        /><MetricChart
          v-if="series('sensor').length"
          title="Temperature"
          subtitle="Readable hardware sensors"
          icon="activity"
          metric="temperature.celsius"
          :series="series('sensor')"
          unit="°C"
          color="#dfaa63"
          :resolution="resolution"
          :theme="theme"
        /></div
    ></template>
  </div>
</template>
