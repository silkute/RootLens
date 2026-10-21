<script setup lang="ts">
withDefaults(defineProps<{ name: string; size?: number }>(), { size: 18 });
const paths: Record<string, string> = {
  lens: "M9 3H5a2 2 0 0 0-2 2v4m12-6h4a2 2 0 0 1 2 2v4M3 15v4a2 2 0 0 0 2 2h4m12-6v4a2 2 0 0 1-2 2h-4M8 12a4 4 0 1 0 8 0 4 4 0 0 0-8 0",
  server: "M4 3h16v7H4zM4 14h16v7H4zM7 6.5h.01M7 17.5h.01M11 6.5h6M11 17.5h6",
  activity: "M2 12h4l3-8 5 16 3-8h5",
  cpu: "M7 7h10v10H7zM10 1v3m4-3v3M10 20v3m4-3v3M1 10h3m-3 4h3M20 10h3m-3 4h3M4 4h16v16H4z",
  memory: "M3 6h18v12H3zM7 9v5m5-5v5m5-5v5M7 18v3m5-3v3m5-3v3",
  disk: "M4 3h16l2 11v7H2v-7L4 3zM2 14h20M6 17.5h.01M10 17.5h.01",
  network: "M12 3v12M8 7l4-4 4 4M4 12v9m-3-3 3 3 3-3M20 12v9m-3-3 3 3 3-3",
  search: "M21 21l-5-5M3 10a7 7 0 1 0 14 0 7 7 0 0 0-14 0",
  plus: "M12 5v14M5 12h14",
  chevron: "m9 5 7 7-7 7",
  down: "m6 9 6 6 6-6",
  check: "m5 12 4 4L19 6",
  clock: "M12 8v5l3 2M2 12a10 10 0 1 0 20 0 10 10 0 0 0-20 0",
  moon: "M20.9 13a9 9 0 0 1-9.9-9.9A9 9 0 1 0 20.9 13z",
  sun: "M8 12a4 4 0 1 0 8 0 4 4 0 0 0-8 0M12 2v2m0 16v2M2 12h2m16 0h2M5 5l1 1m12 12 1 1M5 19l1-1M18 6l1-1",
  settings:
    "M9 3h6l1 3 3 1 2 5-2 5-3 1-1 3H9l-1-3-3-1-2-5 2-5 3-1 1-3zM9 12a3 3 0 1 0 6 0 3 3 0 0 0-6 0",
  alert: "m12 3 10 18H2L12 3zM12 9v5m0 3h.01",
  box: "m12 2 10 5v10l-10 5L2 17V7l10-5zM2 7l10 5 10-5M12 12v10M7 4.5l10 5",
  arrow: "M4 12h16m-6-6 6 6-6 6",
  refresh:
    "M20 7V3m0 4h-4M4 17v4m0-4h4M4 10a8 8 0 0 1 14-5l2 2M20 14a8 8 0 0 1-14 5l-2-2",
  info: "M12 11v6m0-10h.01M2 12a10 10 0 1 0 20 0 10 10 0 0 0-20 0",
  download: "M12 3v12m-4-4 4 4 4-4M4 17v4h16v-4",
  close: "m6 6 12 12M6 18 18 6",
  shield: "m12 2 9 4v6c0 5-9 10-9 10S3 17 3 12V6l9-4zm-5 10 3 3 7-7",
  logout: "M9 3H3v18h6M9 12h12m-4-4 4 4-4 4",
};
</script>
<template>
  <svg
    :width="size"
    :height="size"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="1.65"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <path :d="paths[name] || paths.activity" />
  </svg>
</template>
