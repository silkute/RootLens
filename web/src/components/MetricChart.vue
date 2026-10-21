<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import uPlot from "uplot";
import "uplot/dist/uPlot.min.css";
import type { Incident, MetricSeries } from "../types";
import { number } from "../api";
import Icon from "./Icon.vue";

const props = defineProps<{
  title: string;
  subtitle: string;
  icon: string;
  metric: string;
  series: MetricSeries[];
  unit: string;
  color: string;
  resolution: number;
  incidents?: Incident[];
  theme: string;
}>();
const element = ref<HTMLDivElement>();
const showTable = ref(false);
const showPeak = ref(false);
let plot: uPlot | undefined;
let observer: ResizeObserver | undefined;
const current = computed(
  () =>
    props.series[0]?.points
      .filter((p) => p.values[props.metric] !== undefined)
      .at(-1)?.values[props.metric],
);
const label = computed(() => number(current.value, props.unit));
const colors = computed(() => {
  const light: Record<string, string> = {
    "#6e9dec": "#386fc2",
    "#56b5a9": "#287e74",
    "#dfaa63": "#96601b",
    "#9c8bd9": "#7560b1",
  };
  return props.theme === "light"
    ? [
        light[props.color] || "#386fc2",
        "#7560b1",
        "#287e74",
        "#96601b",
        "#51657f",
      ]
    : [props.color, "#9a8bea", "#54b6b2", "#e4a759", "#889ab6"];
});
function makeData(): uPlot.AlignedData {
  const times = new Set<number>();
  for (const s of props.series.slice(0, 5))
    for (const p of s.points) times.add(Date.parse(p.at) / 1000);
  const sorted = [...times].sort((a, b) => a - b);
  const expanded: number[] = [];
  sorted.forEach((t, i) => {
    if (i && t - sorted[i - 1]! > props.resolution * 1.5)
      expanded.push(sorted[i - 1]! + props.resolution);
    expanded.push(t);
  });
  const values = props.series.slice(0, 5).map((s) => {
    const m = new Map(s.points.map((p) => [Date.parse(p.at) / 1000, p]));
    let generation: string | undefined;
    return expanded.map((t) => {
      const p = m.get(t);
      if (!p || p.coverage < 0.5) return null;
      if (generation && generation !== p.generation) {
        generation = p.generation;
        return null;
      }
      generation = p.generation;
      return p.values[props.metric] ?? null;
    });
  });
  if (showPeak.value && props.series[0]) {
    const peaks = new Map(
      props.series[0].points.map((p) => [Date.parse(p.at) / 1000, p]),
    );
    values.push(
      expanded.map((t) => {
        const p = peaks.get(t);
        return p && p.coverage >= 0.5 ? (p.max?.[props.metric] ?? null) : null;
      }),
    );
  }
  return [expanded, ...values] as uPlot.AlignedData;
}
function draw() {
  if (!element.value) return;
  plot?.destroy();
  plot = undefined;
  const data = makeData();
  if (!data[0].length || !props.series.length) return;
  const styles = getComputedStyle(document.documentElement);
  const muted = styles.getPropertyValue("--muted").trim();
  const border = styles.getPropertyValue("--border").trim();
  plot = new uPlot(
    {
      width: element.value.clientWidth,
      height: 190,
      padding: [12, 12, 0, 0],
      cursor: { sync: { key: "rootlens-time" }, drag: { x: false, y: false } },
      select: { show: false, left: 0, top: 0, width: 0, height: 0 },
      legend: { show: false },
      series: [
        {},
        ...props.series.slice(0, 5).map((s, i) => ({
          label: s.entity.name,
          stroke: colors.value[i],
          width: 1.6,
          fill: i === 0 ? `${colors.value[i]}18` : undefined,
          spanGaps: false,
          points: { show: false },
        })),
        ...(showPeak.value
          ? [
              {
                label: "Peak bucket value",
                stroke: colors.value[0],
                width: 1,
                dash: [4, 4],
                spanGaps: false,
                points: { show: false },
              },
            ]
          : []),
      ],
      scales: {
        x: { time: true },
        y: {
          auto: true,
          range: (_u, min, max) => [
            Math.min(0, min),
            Math.max(max * 1.15, props.unit === "%" ? 5 : 1),
          ],
        },
      },
      axes: [
        {
          stroke: muted,
          grid: { show: false },
          ticks: { show: false },
          font: "11px Inter",
          size: 28,
          values: (_u, ticks) =>
            ticks.map((t) =>
              new Date(t * 1000).toLocaleTimeString([], {
                hour: "2-digit",
                minute: "2-digit",
              }),
            ),
        },
        {
          stroke: muted,
          grid: { stroke: border, width: 1 },
          ticks: { show: false },
          font: "11px Inter",
          size: 51,
          values: (_u, ticks) =>
            ticks.map((t) => number(t, props.unit).replace(" ", "\u00a0")),
        },
      ],
      hooks: {
        draw: [
          (u) => {
            const ctx = u.ctx;
            for (const incident of props.incidents || []) {
              const x1 = u.valToPos(
                Date.parse(incident.start) / 1000,
                "x",
                true,
              );
              const x2 = u.valToPos(
                Date.parse(incident.end || incident.updated) / 1000,
                "x",
                true,
              );
              const left = Math.max(x1, u.bbox.left);
              const right = Math.min(x2, u.bbox.left + u.bbox.width);
              if (right > left) {
                ctx.fillStyle = "#e7aa5810";
                ctx.fillRect(left, u.bbox.top, right - left, u.bbox.height);
              }
            }
          },
        ],
      },
    },
    data,
    element.value,
  );
}
onMounted(() => {
  observer = new ResizeObserver(() => draw());
  if (element.value) observer.observe(element.value);
  draw();
});
watch(() => [props.series, props.theme, props.metric, showPeak.value], draw, {
  deep: false,
  flush: "post",
});
onBeforeUnmount(() => {
  observer?.disconnect();
  plot?.destroy();
});
</script>
<template>
  <section class="card chart-card">
    <div class="chart-heading">
      <div>
        <h3><Icon :name="icon" :size="16" />{{ title }}</h3>
        <p>{{ subtitle }}</p>
      </div>
      <strong :style="{ color }">{{ label }}</strong>
    </div>
    <div
      ref="element"
      class="chart-canvas"
      role="img"
      :aria-label="`${title}. Latest ${label}. Historical measurements available in the data table.`"
    ></div>
    <div v-if="current === undefined" class="chart-empty">
      No supported measurements in this interval
    </div>
    <div class="chart-bottom">
      <span
        ><i class="legend-dot" :style="{ background: colors[0] }"></i
        >{{ series[0]?.entity.name || "No data"
        }}<span v-if="series.length > 1"> + {{ series.length - 1 }}</span></span
      ><label class="checkbox-label"
        ><input v-model="showPeak" type="checkbox" />Peak</label
      ><button
        class="text-button"
        @click="showTable = !showTable"
        :aria-expanded="showTable"
      >
        {{ showTable ? "Hide data" : "View data" }}
      </button>
    </div>
    <div v-if="showTable" class="chart-data">
      <table>
        <caption>
          {{
            title
          }}
          ·
          {{
            resolution
          }}-second buckets
        </caption>
        <thead>
          <tr>
            <th>Time</th>
            <th>Value</th>
            <th>Coverage</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="point in series[0]?.points.slice(-50)"
            :key="point.at + point.generation"
          >
            <td>{{ new Date(point.at).toLocaleTimeString() }}</td>
            <td>{{ number(point.values[metric], unit) }}</td>
            <td>{{ Math.round(point.coverage * 100) }}%</td>
          </tr>
        </tbody>
      </table>
      <p class="muted">
        Most recent 50 buckets. Missing measurements are not zero.
      </p>
    </div>
  </section>
</template>
