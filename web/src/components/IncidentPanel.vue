<script setup lang="ts">
import type { Incident } from "../types";
import { clock, date, number } from "../api";
import Icon from "./Icon.vue";
defineProps<{ incident: Incident; hostName?: string; compact?: boolean }>();
defineEmits<{ inspect: [incident: Incident] }>();
const levels: Record<string, string> = {
  measured_fact: "Measured fact",
  strongly_supported_inference: "Supported inference",
  possible_contributor: "Possible contributor",
  insufficient_evidence: "Insufficient evidence",
};
function intervalLink(incident: Incident) {
  const from = new Date(Date.parse(incident.start) - 600000).toISOString();
  const to = new Date(
    Math.min(Date.now(), Date.parse(incident.end || incident.updated) + 300000),
  ).toISOString();
  return `#host/${incident.host_id}/summary?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`;
}
</script>
<template>
  <article class="card incident-panel" :class="{ compact }">
    <div class="incident-top">
      <div
        class="incident-symbol"
        :class="incident.state === 'open' ? 'warning' : 'subtle'"
      >
        <Icon :name="incident.state === 'resolved' ? 'check' : 'activity'" />
      </div>
      <div class="grow">
        <h3>{{ incident.title }}</h3>
        <p>
          {{ hostName || incident.host_id }} <span class="separator">·</span>
          {{ date(incident.start)
          }}<span v-if="incident.end"> – {{ clock(incident.end) }}</span>
        </p>
      </div>
      <span class="badge" :class="incident.state">{{ incident.state }}</span>
    </div>
    <template v-if="compact"
      ><p class="incident-excerpt">
        {{
          incident.evidence[0]?.text ||
          "Review the measurements recorded during this interval."
        }}
      </p>
      <div class="incident-footer">
        <span class="muted small"
          >{{ incident.evidence.length }} evidence signal{{
            incident.evidence.length === 1 ? "" : "s"
          }}
          <span class="separator">·</span> {{ incident.resource }}</span
        ><button class="text-button accent" @click="$emit('inspect', incident)">
          Investigate <Icon name="arrow" :size="15" />
        </button></div
    ></template>
    <template v-else>
      <div class="evidence-section">
        <h4>What the evidence shows</h4>
        <div
          v-for="(e, index) in incident.evidence"
          :key="index"
          class="evidence-row"
        >
          <Icon name="check" :size="16" />
          <div>
            <span class="evidence-label">{{ levels[e.level] || e.level }}</span>
            <p>{{ e.text }}</p>
            <small v-if="e.coverage !== undefined" class="muted"
              >{{ Math.round(e.coverage * 100) }}% measurement coverage</small
            >
          </div>
        </div>
      </div>
      <div class="evidence-section">
        <h4>Observed workloads</h4>
        <div v-if="!incident.contributors.length" class="notice">
          <Icon name="info" />
          <p>
            Reliable workload attribution is unavailable. RootLens cannot
            identify a responsible container from these measurements.
          </p>
        </div>
        <div
          v-for="c in incident.contributors"
          :key="c.entity_id + c.device"
          class="contributor"
        >
          <div class="split">
            <strong>{{ c.name }}</strong
            ><span class="badge subtle">{{ c.label }}</span>
          </div>
          <div class="contributor-values">
            <span
              >{{ number(c.baseline, "rate") }} <Icon name="arrow" :size="13" />
              {{ number(c.activity, "rate") }}</span
            ><span
              >{{ c.share.toFixed(1) }}% of observed container activity</span
            >
          </div>
          <p class="muted small">
            Device {{ c.device }} · {{ Math.round(c.coverage * 100) }}% coverage
            · account-level block I/O, not application file access
          </p>
        </div>
      </div>
      <div class="evidence-section">
        <h4>Suggested investigation</h4>
        <ol class="steps">
          <li v-for="step in incident.steps" :key="step">{{ step }}</li>
        </ol>
      </div>
      <div class="notice">
        <Icon name="info" />
        <div>
          <p v-for="limitation in incident.limitations" :key="limitation">
            {{ limitation }}
          </p>
        </div>
      </div>
      <div class="incident-footer">
        <a class="text-button" :href="intervalLink(incident)"
          >View this interval<Icon name="arrow" :size="14" /></a
        ><span class="muted small">Rule v{{ incident.rule_version }}</span
        ><a
          class="button small-button"
          :href="`/api/v1/incidents/${incident.id}/report`"
          ><Icon name="download" :size="15" /> Export report</a
        >
      </div>
    </template>
  </article>
</template>
