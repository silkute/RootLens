<script setup lang="ts">
import {
  computed,
  onBeforeUnmount,
  onMounted,
  reactive,
  ref,
  watch,
} from "vue";
import {
  DialogRoot,
  DialogPortal,
  DialogOverlay,
  DialogContent,
  DialogTitle,
  DialogDescription,
  DialogClose,
} from "reka-ui";
import {
  api,
  APIError,
  clock,
  date,
  mutate,
  number,
  relative,
  setCSRF,
} from "./api";
import type {
  Container,
  Event,
  Host,
  Incident,
  MetricsResponse,
  MetricSeries,
  Retention,
} from "./types";
import Icon from "./components/Icon.vue";
import MetricChart from "./components/MetricChart.vue";
import IncidentPanel from "./components/IncidentPanel.vue";
import AdditionalMetrics from "./components/AdditionalMetrics.vue";
import ProcessSummary from "./components/ProcessSummary.vue";

const session = ref<{
  authenticated?: boolean;
  setup_required?: boolean;
  demo?: boolean;
  username?: string;
  csrf?: string;
} | null>(null);
const hosts = ref<Host[]>([]);
const incidents = ref<Incident[]>([]);
const metrics = ref<MetricsResponse | null>(null);
const containers = ref<Container[]>([]);
const events = ref<Event[]>([]);
const loading = ref(true);
const error = ref("");
const busy = ref(false);
const search = ref("");
const hostFilter = ref("");
const stateFilter = ref("");
const resourceFilter = ref("");
const containerSearch = ref("");
const containerSort = ref("name");
const sortHosts = ref("name");
const hash = ref(location.hash.slice(1) || "overview");
const theme = ref(
  localStorage.getItem("rootlens-theme") ||
    (matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light"),
);
const notify = ref(localStorage.getItem("rootlens-notify") !== "off");
const activeOnly = ref(false);
const selectedContainer = ref("");
const showCores = ref(false);
const notice = ref("");
const addOpen = ref(false);
const addName = ref("");
const enrollment = ref<{
  token: string;
  host_id: string;
  expires: string;
} | null>(null);
const account = reactive({ username: "", password: "", token: "" });
const accountBusy = ref(false);
const retention = ref<Retention>({
  raw_hours: 48,
  minute_days: 14,
  quarter_days: 180,
  event_days: 180,
});
const health = ref<Record<string, any>>({});
const managedHost = ref("");
const credentials = ref<{ id: string; created: string; revoked: boolean }[]>(
  [],
);
const investigation = ref<Incident[] | null>(null);
const analysisBusy = ref(false);
const customFrom = ref("");
const customTo = ref("");
const customRange = ref(false);
const parts = computed(() => hash.value.split("?")[0]!.split("/"));
const page = computed(() => parts.value[0] || "overview");
const hostID = computed(() =>
  page.value === "host" ? parts.value[1] || "" : "",
);
const tab = computed(() => parts.value[2] || "summary");
const range = computed(
  () => new URLSearchParams(hash.value.split("?")[1]).get("range") || "1h",
);
const host = computed(() => hosts.value.find((h) => h.id === hostID.value));
const selectedIncident = computed(() =>
  incidents.value.find((i) => i.id === parts.value[1]),
);
const activeIncidents = computed(() =>
  incidents.value.filter((i) => i.state === "open"),
);
const hostIncidents = computed(() =>
  incidents.value.filter((i) => i.host_id === hostID.value),
);
const findings = computed(() =>
  hostIncidents.value.filter((i) => i.state === "open"),
);
const online = computed(
  () => hosts.value.filter((h) => h.status === "online").length,
);
const visibleHosts = computed(() =>
  [...hosts.value]
    .filter(
      (h) =>
        h.name.toLowerCase().includes(search.value.toLowerCase()) &&
        (!activeOnly.value || h.active_incidents > 0),
    )
    .sort((a, b) =>
      sortHosts.value === "name"
        ? a.name.localeCompare(b.name)
        : (b.latest[sortHosts.value] || 0) - (a.latest[sortHosts.value] || 0),
    ),
);
const visibleIncidents = computed(() =>
  incidents.value.filter(
    (i) =>
      (!hostFilter.value || i.host_id === hostFilter.value) &&
      (!stateFilter.value || i.state === stateFilter.value) &&
      (!resourceFilter.value || i.resource === resourceFilter.value),
  ),
);
const visibleContainers = computed(() =>
  [...containers.value]
    .filter((c) =>
      c.entity.name.toLowerCase().includes(containerSearch.value.toLowerCase()),
    )
    .sort((a, b) =>
      containerSort.value === "name"
        ? a.entity.name.localeCompare(b.entity.name)
        : (b.values[containerSort.value] || 0) -
          (a.values[containerSort.value] || 0),
    ),
);
const unavailable = computed(() =>
  Object.entries(host.value?.capabilities || {}).filter(
    ([, c]) => c.status !== "available",
  ),
);
const chartSeries = (kind: string): MetricSeries[] =>
  metrics.value?.series.filter(
    (s) => s.entity.kind === kind && s.points.length > 0,
  ) || [];
const hostName = (id: string) =>
  hosts.value.find((h) => h.id === id)?.name || id;
const uptime = computed(() => {
  const seconds = host.value?.latest["uptime.seconds"];
  return seconds === undefined
    ? "Uptime unavailable"
    : `${Math.floor(seconds / 86400)} days uptime`;
});
const resolution = computed(() => metrics.value?.resolution_seconds || 5);
const timestamp = computed(() =>
  metrics.value
    ? `${date(metrics.value.from)} – ${clock(metrics.value.to)}`
    : "Waiting for historical measurements",
);
const hostTabs = [
  { id: "summary", title: "Summary", icon: "activity" },
  { id: "containers", title: "Containers", icon: "box" },
  { id: "diagnostics", title: "Diagnostics", icon: "lens" },
  { id: "timeline", title: "Timeline", icon: "clock" },
];

function skipMain() {
  document.getElementById("main-content")?.focus();
}
function go(route: string) {
  location.hash = route;
}
function routeHost(id: string, nextTab = "summary") {
  const query = new URLSearchParams(hash.value.split("?")[1]);
  if (!query.has("range")) query.set("range", range.value);
  return `host/${id}/${nextTab}?${query}`;
}
function changeRange(value: string) {
  go(`host/${hostID.value}/${tab.value}?range=${value}`);
}
function inspect(incident: Incident) {
  go(`incident/${incident.id}`);
}
function setTheme(value: string) {
  theme.value = value;
  document.documentElement.dataset.theme = value;
  localStorage.setItem("rootlens-theme", value);
}
watch(theme, (value) => setTheme(value), { immediate: true });
watch(notify, (value) =>
  localStorage.setItem("rootlens-notify", value ? "on" : "off"),
);
const handleHash = () => {
  hash.value = location.hash.slice(1) || "overview";
};
let timer: ReturnType<typeof setInterval>;
let requestGeneration = 0;
let polling = false;
function bounds() {
  const query = new URLSearchParams(hash.value.split("?")[1]);
  if (query.has("from") && query.has("to"))
    return { from: query.get("from")!, to: query.get("to")! };
  const to = new Date();
  const hours: Record<string, number> = {
    "1h": 1,
    "6h": 6,
    "24h": 24,
    "7d": 168,
    "30d": 720,
  };
  return {
    from: new Date(
      to.getTime() - (hours[range.value] || 1) * 3600000,
    ).toISOString(),
    to: to.toISOString(),
  };
}
async function refresh(initial = false) {
  if (polling && !initial) return;
  const generation = ++requestGeneration;
  polling = true;
  try {
    const results = await Promise.allSettled([
      api<Host[]>("/hosts"),
      api<Incident[]>("/incidents"),
    ]);
    if (generation !== requestGeneration) return;
    if (results[0].status === "fulfilled") hosts.value = results[0].value;
    else throw results[0].reason;
    if (results[1].status === "fulfilled") incidents.value = results[1].value;
    else throw results[1].reason;
    const id = hostID.value;
    if (id) {
      const b = bounds();
      const result = await Promise.allSettled([
        api<MetricsResponse>(
          `/hosts/${id}/metrics?from=${encodeURIComponent(b.from)}&to=${encodeURIComponent(b.to)}${selectedContainer.value ? `&entity=${selectedContainer.value}` : ""}`,
        ),
        api<Container[]>(`/hosts/${id}/containers`),
        api<Event[]>(`/events?host=${id}`),
      ]);
      if (generation !== requestGeneration) return;
      if (result[0].status === "fulfilled") metrics.value = result[0].value;
      if (result[1].status === "fulfilled") containers.value = result[1].value;
      if (result[2].status === "fulfilled") events.value = result[2].value;
      const failed = result.find((r) => r.status === "rejected");
      if (failed?.status === "rejected") throw failed.reason;
    }
    if (page.value === "incident" && !selectedIncident.value)
      incidents.value.push(await api<Incident>(`/incidents/${parts.value[1]}`));
    if (page.value === "settings" && initial) {
      retention.value = await api<Retention>("/settings");
      health.value = await api("/health/storage");
    }
    error.value = "";
  } catch (e) {
    if (generation !== requestGeneration) return;
    error.value =
      e instanceof Error ? e.message : "Unable to refresh measurements.";
    if (e instanceof APIError && e.status === 401) {
      session.value = await api("/me");
      setCSRF("");
    }
  } finally {
    if (generation === requestGeneration) {
      loading.value = false;
      polling = false;
    }
  }
}
async function boot() {
  try {
    session.value = await api("/me");
    setCSRF(session.value?.csrf || "");
    if (session.value?.authenticated || session.value?.demo)
      await refresh(true);
    else loading.value = false;
  } catch (e) {
    error.value = e instanceof Error ? e.message : "RootLens is unavailable.";
    loading.value = false;
  }
}
watch(hash, () => {
  metrics.value = null;
  containers.value = [];
  selectedContainer.value = "";
  investigation.value = null;
  customRange.value = false;
  refresh(true);
});
async function login() {
  accountBusy.value = true;
  error.value = "";
  try {
    if (session.value?.setup_required) await mutate("/setup", "POST", account);
    const result = await mutate<{ csrf: string }>("/session", "POST", {
      username: account.username,
      password: account.password,
    });
    setCSRF(result.csrf);
    account.password = "";
    account.token = "";
    await boot();
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    accountBusy.value = false;
  }
}
async function logout() {
  try {
    await mutate("/session", "DELETE");
    session.value = { authenticated: false };
    setCSRF("");
  } catch (e) {
    error.value = (e as Error).message;
  }
}
async function addHost() {
  busy.value = true;
  notice.value = "";
  try {
    const created = await mutate<{ id: string }>("/hosts", "POST", {
      name: addName.value,
    });
    enrollment.value = await mutate(
      `/hosts/${created.id}/enrollment-tokens`,
      "POST",
    );
    await refresh(true);
  } catch (e) {
    notice.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
async function enrollExisting(id: string) {
  busy.value = true;
  try {
    enrollment.value = await mutate(`/hosts/${id}/enrollment-tokens`, "POST");
    addOpen.value = true;
  } catch (e) {
    notice.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
function downloadToken() {
  if (!enrollment.value) return;
  const objectURL = URL.createObjectURL(
    new Blob([enrollment.value.token + "\n"], { type: "text/plain" }),
  );
  const a = document.createElement("a");
  a.href = objectURL;
  a.download = "enrollment.token";
  a.click();
  URL.revokeObjectURL(objectURL);
}
async function loadCredentials() {
  try {
    credentials.value = managedHost.value
      ? await api(`/hosts/${managedHost.value}/credentials`)
      : [];
  } catch (e) {
    notice.value = (e as Error).message;
  }
}
async function revoke(id: string) {
  try {
    await mutate(`/hosts/${managedHost.value}/credentials/${id}`, "DELETE");
    await loadCredentials();
    notice.value =
      "Credential revoked. The agent can no longer upload telemetry.";
  } catch (e) {
    notice.value = (e as Error).message;
  }
}
async function saveRetention() {
  busy.value = true;
  try {
    retention.value = await mutate("/settings", "PATCH", retention.value);
    notice.value = "Retention settings saved. Cleanup applies incrementally.";
  } catch (e) {
    notice.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
async function investigate() {
  analysisBusy.value = true;
  try {
    const b = customRange.value
      ? {
          from: new Date(customFrom.value).toISOString(),
          to: new Date(customTo.value).toISOString(),
        }
      : bounds();
    const result = await mutate<{ findings: Incident[] }>(
      `/hosts/${hostID.value}/investigations`,
      "POST",
      b,
    );
    investigation.value = result.findings;
  } catch (e) {
    notice.value = (e as Error).message;
  } finally {
    analysisBusy.value = false;
  }
}
onMounted(() => {
  window.addEventListener("hashchange", handleHash);
  boot();
  timer = setInterval(() => {
    if (
      !document.hidden &&
      (session.value?.authenticated || session.value?.demo)
    )
      refresh();
  }, 5000);
});
onBeforeUnmount(() => {
  clearInterval(timer);
  window.removeEventListener("hashchange", handleHash);
});
</script>

<template>
  <a class="skip-link" href="#main-content" @click.prevent="skipMain"
    >Skip to content</a
  >
  <div
    v-if="session && !session.authenticated && !session.demo"
    class="login-layout"
  >
    <div class="login-card card">
      <a href="#overview" class="brand"
        ><span class="brand-mark"><Icon name="lens" :size="21" /></span
        >RootLens</a
      >
      <h1>
        {{ session.setup_required ? "Welcome to RootLens" : "Welcome back" }}
      </h1>
      <p class="muted">
        {{
          session.setup_required
            ? "Create your local administrator account."
            : "Sign in to understand your infrastructure."
        }}
      </p>
      <form @submit.prevent="login" class="form-stack">
        <label v-if="session.setup_required"
          >Setup token<input
            v-model="account.token"
            type="password"
            autocomplete="off"
            required /></label
        ><label
          >Username<input
            v-model="account.username"
            autocomplete="username"
            maxlength="64"
            required /></label
        ><label
          >Password<input
            v-model="account.password"
            type="password"
            :autocomplete="
              session.setup_required ? 'new-password' : 'current-password'
            "
            minlength="12"
            maxlength="128"
            required
        /></label>
        <p v-if="error" class="form-error" role="alert">{{ error }}</p>
        <button class="button primary" :disabled="accountBusy">
          {{
            accountBusy
              ? "Please wait…"
              : session.setup_required
                ? "Create account"
                : "Sign in"
          }}<Icon name="arrow" :size="16" />
        </button>
      </form>
      <p class="login-note">
        <Icon name="shield" :size="14" />Self-hosted. Your data stays with you.
      </p>
    </div>
  </div>
  <div v-else class="app-layout">
    <header class="topbar">
      <div class="topbar-inner">
        <a href="#overview" class="brand"
          ><span class="brand-mark"><Icon name="lens" :size="21" /></span
          >RootLens<span class="version">v0.1</span></a
        >
        <nav class="global-nav" aria-label="Main navigation">
          <a
            href="#overview"
            :class="{ active: page === 'overview' || page === 'host' }"
            ><Icon name="server" :size="16" />Overview</a
          ><a
            href="#incidents"
            :class="{ active: page === 'incidents' || page === 'incident' }"
            ><Icon name="activity" :size="16" />Incidents<span
              v-if="activeIncidents.length && notify"
              class="nav-count"
              >{{ activeIncidents.length }}</span
            ></a
          ><a href="#settings" :class="{ active: page === 'settings' }"
            ><Icon name="settings" :size="16" />Settings</a
          >
        </nav>
        <div class="topbar-actions">
          <span v-if="session?.demo" class="demo-label">Demo workspace</span
          ><button
            class="icon-button"
            @click="theme = theme === 'dark' ? 'light' : 'dark'"
            :aria-label="
              theme === 'dark' ? 'Switch to light mode' : 'Switch to dark mode'
            "
          >
            <Icon :name="theme === 'dark' ? 'sun' : 'moon'" /></button
          ><button
            v-if="!session?.demo && session?.authenticated"
            class="icon-button"
            @click="logout"
            aria-label="Sign out"
          >
            <Icon name="logout" />
          </button>
        </div>
      </div>
    </header>
    <main id="main-content" class="main-content" tabindex="-1">
      <div v-if="error" class="notice error-banner" role="alert">
        <Icon name="alert" />
        <div>
          <strong>Unable to refresh</strong>
          <p>{{ error }} Existing measurements may be stale.</p>
        </div>
        <button class="button small-button" @click="refresh(true)">
          Retry
        </button>
      </div>
      <div v-if="notice" class="notice status-notice" role="status">
        <Icon name="info" />
        <p>{{ notice }}</p>
        <button
          class="icon-button"
          aria-label="Dismiss notice"
          @click="notice = ''"
        >
          <Icon name="close" :size="16" />
        </button>
      </div>
      <div v-if="loading" class="loading-state" aria-label="Loading dashboard">
        <div class="skeleton heading-skeleton"></div>
        <div class="skeleton table-skeleton"></div>
      </div>
      <template v-else-if="page === 'overview'">
        <div class="page-heading">
          <div>
            <div class="eyebrow">
              <span class="live-dot"></span>Your infrastructure, in focus
            </div>
            <h1>System overview</h1>
            <p class="muted">
              See what’s running. Understand what’s slowing it down.
            </p>
          </div>
          <button
            class="button"
            :disabled="session?.demo"
            @click="
              addOpen = true;
              enrollment = null;
              addName = '';
            "
          >
            <Icon name="plus" :size="16" />Add system
          </button>
        </div>
        <div class="overview-strip">
          <span
            ><Icon name="server" :size="15" /><strong>{{
              hosts.length
            }}</strong>
            systems</span
          ><span
            ><span class="status-dot online"></span
            ><strong>{{ online }}</strong> online</span
          ><button
            class="strip-link"
            :class="{ amber: activeIncidents.length }"
            @click="go('incidents')"
          >
            <Icon name="activity" :size="15" /><strong>{{
              activeIncidents.length
            }}</strong>
            active incident{{ activeIncidents.length === 1 ? "" : "s" }}</button
          ><span class="strip-end"
            ><Icon name="refresh" :size="13" />Updates every 5s</span
          >
        </div>
        <section class="card systems-card">
          <div class="card-toolbar">
            <div class="search-field">
              <Icon name="search" :size="16" /><input
                v-model="search"
                aria-label="Filter systems"
                placeholder="Filter systems…"
              />
            </div>
            <label class="checkbox-label"
              ><input v-model="activeOnly" type="checkbox" />With
              incidents</label
            >
          </div>
          <div v-if="!hosts.length" class="empty-state">
            <span class="empty-icon"><Icon name="server" :size="27" /></span>
            <h2>Your first system starts here</h2>
            <p>
              Connect a Linux agent to see measurements, history, and
              evidence-backed diagnostics.
            </p>
            <button class="button primary" @click="addOpen = true">
              <Icon name="plus" :size="16" />Add your first system
            </button>
          </div>
          <div v-else class="table-wrap">
            <table class="systems-table">
              <thead>
                <tr>
                  <th>
                    <button @click="sortHosts = 'name'">
                      System <Icon name="down" :size="12" />
                    </button>
                  </th>
                  <th>
                    <button @click="sortHosts = 'cpu.busy_pct'">
                      <Icon name="cpu" :size="14" />CPU
                      <Icon name="down" :size="12" />
                    </button>
                  </th>
                  <th>
                    <button @click="sortHosts = 'memory.used_pct'">
                      <Icon name="memory" :size="14" />Memory
                    </button>
                  </th>
                  <th class="disk-column">
                    <Icon name="disk" :size="14" />Disk space
                  </th>
                  <th>Pressure</th>
                  <th class="row-action">
                    <span class="sr-only">Open system</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="system in visibleHosts" :key="system.id">
                  <td>
                    <a :href="`#${routeHost(system.id)}`" class="system-name"
                      ><span class="server-tile"
                        ><Icon name="server" :size="19" /></span
                      ><span
                        ><strong>{{ system.name }}</strong
                        ><span class="system-sub"
                          ><span
                            class="status-dot"
                            :class="system.status"
                          ></span
                          >{{ system.status }}<span class="separator">·</span
                          >{{ system.agent_version || "Awaiting agent" }}</span
                        ></span
                      ></a
                    >
                  </td>
                  <td>
                    <div class="metric-cell">
                      <span>{{
                        number(system.latest["cpu.busy_pct"], "%")
                      }}</span>
                      <div class="resource-bar">
                        <i
                          :style="{
                            width: `${Math.min(100, system.latest['cpu.busy_pct'] || 0)}%`,
                            background: 'var(--blue)',
                          }"
                        ></i>
                      </div>
                    </div>
                  </td>
                  <td>
                    <div class="metric-cell">
                      <span>{{
                        number(system.latest["memory.used_pct"], "%")
                      }}</span>
                      <div class="resource-bar">
                        <i
                          :style="{
                            width: `${Math.min(100, system.latest['memory.used_pct'] || 0)}%`,
                            background: 'var(--teal)',
                          }"
                        ></i>
                      </div>
                    </div>
                  </td>
                  <td class="disk-column">
                    <div class="metric-cell">
                      <span>{{
                        number(system.latest["fs.used_pct"], "%")
                      }}</span>
                      <div class="resource-bar">
                        <i
                          :style="{
                            width: `${Math.min(100, system.latest['fs.used_pct'] || 0)}%`,
                            background: 'var(--violet)',
                          }"
                        ></i>
                      </div>
                    </div>
                  </td>
                  <td>
                    <a
                      v-if="system.active_incidents"
                      :href="`#${routeHost(system.id, 'diagnostics')}`"
                      class="pressure-state amber"
                      ><Icon name="activity" :size="15" />{{
                        system.latest["io.some_pct"] >= 10
                          ? "I/O pressure"
                          : "Investigate"
                      }}</a
                    ><span
                      v-else-if="system.status === 'online'"
                      class="pressure-state muted"
                      ><Icon name="check" :size="15" />No finding</span
                    ><span v-else class="muted">Unavailable</span>
                  </td>
                  <td>
                    <a
                      :href="`#${routeHost(system.id)}`"
                      class="icon-button"
                      :aria-label="`Open ${system.name}`"
                      ><Icon name="chevron" :size="16"
                    /></a>
                  </td>
                </tr>
              </tbody>
            </table>
            <div v-if="!visibleHosts.length" class="empty-state small-empty">
              No systems match this filter.
            </div>
          </div>
          <div class="table-footer">
            <span>{{ visibleHosts.length }} of {{ hosts.length }} systems</span
            ><span
              ><Icon name="shield" :size="13" />Local collection · No cloud
              dependency</span
            >
          </div>
        </section>
        <div class="section-heading">
          <div>
            <h2>
              Recent incidents
              <span class="count-label">{{ incidents.length }}</span>
            </h2>
            <p class="muted">Evidence and context, not just another alert.</p>
          </div>
          <a href="#incidents" class="text-button"
            >View timeline<Icon name="arrow" :size="15"
          /></a>
        </div>
        <div v-if="incidents.length" class="incident-grid">
          <IncidentPanel
            v-for="incident in incidents.slice(0, 2)"
            :key="incident.id"
            :incident="incident"
            :host-name="hostName(incident.host_id)"
            compact
            @inspect="inspect"
          />
        </div>
        <div v-else class="card empty-state small-empty">
          <Icon name="check" :size="23" />
          <h3>No incidents detected</h3>
          <p>
            Supported rules will surface sustained pressure and instability
            here.
          </p>
        </div>
        <div class="insight-note">
          <span class="insight-icon"><Icon name="lens" :size="18" /></span>
          <div>
            <strong>Activity isn’t always a bottleneck.</strong>
            <p>
              RootLens looks for measured pressure and supporting evidence
              before suggesting what to investigate.
            </p>
          </div>
          <a href="#incidents" class="text-button"
            >Explore diagnostics<Icon name="arrow" :size="14"
          /></a>
        </div>
      </template>

      <template v-else-if="page === 'host' && host">
        <div class="breadcrumb">
          <a href="#overview">Systems</a
          ><Icon name="chevron" :size="12" /><span>{{ host.name }}</span>
        </div>
        <div class="host-heading">
          <div class="host-title">
            <span class="server-tile large"
              ><Icon name="server" :size="25"
            /></span>
            <div>
              <h1>
                {{ host.name }}
                <span class="badge" :class="host.status"
                  ><span class="status-dot" :class="host.status"></span
                  >{{ host.status }}</span
                >
              </h1>
              <p class="host-meta">
                Linux <span class="separator">·</span>
                {{ host.latest["cpu.count"] || "—" }} logical cores
                <span class="separator">·</span>
                {{ number(host.latest["memory.total_bytes"], "bytes") }}
                <span class="separator">·</span> {{ uptime }}
              </p>
            </div>
          </div>
          <label class="range-control"
            ><Icon name="clock" :size="15" /><span class="sr-only"
              >Chart time range</span
            ><select
              :value="range"
              @change="changeRange(($event.target as HTMLSelectElement).value)"
            >
              <option value="1h">Last hour</option>
              <option value="6h">Last 6 hours</option>
              <option value="24h">Last 24 hours</option>
              <option value="7d">Last 7 days</option>
              <option value="30d">Last 30 days</option>
            </select></label
          >
        </div>
        <nav class="host-nav" aria-label="System views">
          <a
            v-for="item in hostTabs"
            :key="item.id"
            :href="`#${routeHost(host.id, item.id)}`"
            :class="{ active: tab === item.id }"
            ><Icon :name="item.icon" :size="16" />{{ item.title
            }}<span
              v-if="item.id === 'diagnostics' && findings.length"
              class="nav-count"
              >{{ findings.length }}</span
            ></a
          ><span class="nav-freshness"
            >Updated {{ relative(host.last_source) }}</span
          >
        </nav>
        <div v-if="host.status !== 'online'" class="notice">
          <Icon name="info" />
          <p>
            This agent is {{ host.status }}. Measurements are retained with
            their original timestamps; missing data does not indicate recovery.
          </p>
        </div>
        <template v-if="tab === 'summary'">
          <a
            v-if="findings.length"
            :href="`#${routeHost(host.id, 'diagnostics')}`"
            class="diagnostic-banner"
            ><span class="incident-symbol warning"
              ><Icon name="activity"
            /></span>
            <div>
              <strong>{{ findings[0]!.title }}</strong>
              <p>{{ findings[0]!.evidence[0]?.text }}</p>
            </div>
            <span class="text-button"
              >View evidence<Icon name="arrow" :size="16" /></span
          ></a>
          <div v-else class="quiet-summary">
            <Icon name="check" :size="16" /><span
              >No supported bottleneck detected</span
            ><span class="muted"
              >· This does not establish application health.</span
            >
          </div>
          <div class="chart-grid">
            <MetricChart
              title="CPU usage"
              subtitle="Execution across host CPU capacity"
              icon="cpu"
              metric="cpu.busy_pct"
              :series="chartSeries('host')"
              unit="%"
              color="#6e9dec"
              :resolution="resolution"
              :theme="theme"
            /><MetricChart
              title="Memory usage"
              subtitle="Total memory minus available memory"
              icon="memory"
              metric="memory.used_bytes"
              :series="chartSeries('host')"
              unit="bytes"
              color="#56b5a9"
              :resolution="resolution"
              :theme="theme"
            /><MetricChart
              title="I/O pressure"
              subtitle="Time at least some tasks were stalled"
              icon="activity"
              metric="io.some_pct"
              :series="chartSeries('host')"
              unit="%"
              color="#dfaa63"
              :resolution="resolution"
              :incidents="hostIncidents"
              :theme="theme"
            /><MetricChart
              title="Disk throughput"
              subtitle="Accounted device read bytes per second"
              icon="disk"
              metric="disk.read_bytes"
              :series="chartSeries('device')"
              unit="rate"
              color="#9c8bd9"
              :resolution="resolution"
              :theme="theme"
            /><MetricChart
              title="Request time"
              subtitle="Average completed-request time, not a percentile"
              icon="clock"
              metric="disk.await_ms"
              :series="chartSeries('device')"
              unit="ms"
              color="#dfaa63"
              :resolution="resolution"
              :theme="theme"
            /><MetricChart
              title="Network received"
              subtitle="Host interface throughput"
              icon="network"
              metric="net.rx_bytes"
              :series="chartSeries('network')"
              unit="rate"
              color="#6e9dec"
              :resolution="resolution"
              :theme="theme"
            /><MetricChart
              title="System load"
              subtitle="Runnable and uninterruptible tasks · 1 minute"
              icon="activity"
              metric="load.1"
              :series="chartSeries('host')"
              unit=""
              color="#56b5a9"
              :resolution="resolution"
              :theme="theme"
            /><MetricChart
              title="Disk space"
              subtitle="Filesystem capacity used, not disk activity"
              icon="disk"
              metric="fs.used_pct"
              :series="chartSeries('filesystem')"
              unit="%"
              color="#9c8bd9"
              :resolution="resolution"
              :theme="theme"
            />
          </div>
          <div class="chart-range-note">
            <Icon name="clock" :size="14" />{{ timestamp }} · {{ resolution }}s
            buckets · Gaps mean missing data.
          </div>
          <AdditionalMetrics :host="host" :metrics="metrics" :theme="theme" />
          <ProcessSummary :host-id="host.id" :updated="host.last_source" />
          <details v-if="unavailable.length" class="card capability-card">
            <summary>
              {{ unavailable.length }} collector capabilities need attention
            </summary>
            <div
              v-for="[key, cap] in unavailable"
              :key="key"
              class="capability-row"
            >
              <strong>{{ key }}</strong
              ><span class="badge subtle">{{
                cap.status.replaceAll("_", " ")
              }}</span>
              <p>{{ cap.reason }}</p>
            </div>
          </details>
        </template>
        <template v-else-if="tab === 'containers'">
          <div class="section-heading first-section">
            <div>
              <h2>
                Containers
                <span class="count-label">{{ containers.length }}</span>
              </h2>
              <p class="muted">Resource use and lifecycle, in context.</p>
            </div>
          </div>
          <section class="card">
            <div class="card-toolbar">
              <div class="search-field">
                <Icon name="search" :size="16" /><input
                  v-model="containerSearch"
                  placeholder="Filter containers…"
                  aria-label="Filter containers"
                />
              </div>
              <label class="sort-control"
                >Sort by<select v-model="containerSort">
                  <option value="name">Name</option>
                  <option value="cpu.cores">CPU</option>
                  <option value="memory.used_bytes">Memory</option>
                </select></label
              >
            </div>
            <div v-if="!containers.length" class="empty-state">
              <Icon name="box" :size="28" />
              <h3>No container measurements</h3>
              <p>
                {{
                  host.capabilities["docker"]?.reason ||
                  "Connect the optional Docker collector to view containers."
                }}
              </p>
              <p v-if="!host.capabilities.docker || host.capabilities.docker.status === 'unsupported'">
                To enable Docker monitoring, rerun the installer with
                <code>--docker</code>. Existing installer-managed installations
                are found automatically.
              </p>
            </div>
            <div v-else class="table-wrap">
              <table class="container-table">
                <thead>
                  <tr>
                    <th>Container</th>
                    <th>CPU</th>
                    <th>Charged memory</th>
                    <th>Limit</th>
                    <th>Received</th>
                    <th>Restarts</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="c in visibleContainers" :key="c.entity.id">
                    <td>
                      <button
                        class="container-name"
                        @click="
                          selectedContainer =
                            selectedContainer === c.entity.id
                              ? ''
                              : c.entity.id;
                          refresh(true);
                        "
                      >
                        <Icon name="box" :size="18" /><span
                          ><strong>{{ c.entity.name }}</strong
                          ><small
                            ><span
                              class="status-dot"
                              :class="
                                c.entity.metadata?.status === 'running'
                                  ? 'online'
                                  : 'offline'
                              "
                            ></span
                            >{{ c.entity.metadata?.status || "Unknown" }} ·
                            {{ relative(c.at) }}</small
                          ></span
                        >
                      </button>
                    </td>
                    <td>{{ number(c.values["cpu.cores"]) }} cores</td>
                    <td>
                      {{ number(c.values["memory.used_bytes"], "bytes") }}
                    </td>
                    <td>
                      {{ number(c.values["memory.limit_bytes"], "bytes") }}
                    </td>
                    <td>{{ number(c.values["net.rx_bytes"], "rate") }}</td>
                    <td>{{ c.entity.metadata?.restarts || "—" }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
          </section>
          <div v-if="selectedContainer" class="chart-grid container-charts">
            <MetricChart
              title="Container CPU"
              subtitle="One core means one logical CPU fully used"
              icon="cpu"
              metric="cpu.cores"
              :series="chartSeries('container')"
              unit=""
              color="#6e9dec"
              :resolution="resolution"
              :theme="theme"
            /><MetricChart
              title="Container memory"
              subtitle="Charged cgroup memory"
              icon="memory"
              metric="memory.used_bytes"
              :series="chartSeries('container')"
              unit="bytes"
              color="#56b5a9"
              :resolution="resolution"
              :theme="theme"
            />
          </div>
          <div class="notice spaced">
            <Icon name="info" />
            <p>
              Container I/O is accounted block activity. It does not include
              every file access and may not reconcile with host device totals.
            </p>
          </div>
        </template>
        <template v-else-if="tab === 'diagnostics'">
          <div class="section-heading first-section">
            <div>
              <h2>Evidence, before explanations</h2>
              <p class="muted">
                Measured facts, possible contributors, and what to investigate
                next.
              </p>
            </div>
            <button
              class="button"
              :disabled="analysisBusy || session?.demo"
              @click="investigate"
            >
              <Icon name="lens" :size="16" />{{
                analysisBusy ? "Investigating…" : "Investigate this interval"
              }}
            </button>
          </div>
          <div v-if="!session?.demo" class="investigation-range">
            <label class="checkbox-label"
              ><input v-model="customRange" type="checkbox" />Choose an exact
              interval</label
            ><template v-if="customRange"
              ><label
                >From<input v-model="customFrom" type="datetime-local" /></label
              ><label
                >To<input v-model="customTo" type="datetime-local" /></label
            ></template>
          </div>
          <div v-if="investigation !== null" class="notice">
            <Icon name="info" />
            <p>
              Historical investigation: {{ investigation.length }} finding(s).
              This does not create live notifications.
            </p>
          </div>
          <div
            v-if="(investigation ?? findings).length"
            class="diagnostic-list"
          >
            <IncidentPanel
              v-for="incident in investigation ?? findings"
              :key="incident.id"
              :incident="incident"
              :host-name="host.name"
            />
          </div>
          <div v-else class="card empty-state">
            <Icon name="check" :size="26" />
            <h3>No supported bottleneck detected</h3>
            <p>
              RootLens has not found sustained evidence meeting the available
              rules. Application issues can still exist outside these
              measurements.
            </p>
          </div>
          <div class="section-heading"><h2>Detection coverage</h2></div>
          <div class="card capabilities">
            <div
              v-for="(cap, key) in host.capabilities"
              :key="key"
              class="capability-row"
            >
              <span>{{ key }}</span
              ><span
                class="badge"
                :class="cap.status === 'available' ? 'online' : 'subtle'"
                >{{ cap.status.replaceAll("_", " ") }}</span
              ><small class="muted">{{ cap.reason }}</small>
            </div>
          </div>
        </template>
        <template v-else-if="tab === 'timeline'"
          ><div class="section-heading first-section">
            <div>
              <h2>System timeline</h2>
              <p class="muted">Incidents and observed lifecycle changes.</p>
            </div>
          </div>
          <div class="timeline">
            <IncidentPanel
              v-for="incident in hostIncidents"
              :key="incident.id"
              :incident="incident"
              :host-name="host.name"
              compact
              @inspect="inspect"
            />
            <div v-for="event in events" :key="event.id" class="timeline-event">
              <span class="timeline-dot"></span>
              <div>
                <strong>{{ event.type.replaceAll("_", " ") }}</strong>
                <p>{{ event.message }}</p>
                <time>{{ date(event.at) }}</time>
              </div>
            </div>
            <div
              v-if="!hostIncidents.length && !events.length"
              class="card empty-state"
            >
              No events have been recorded for this system.
            </div>
          </div></template
        >
      </template>

      <template v-else-if="page === 'incidents'">
        <div class="page-heading">
          <div>
            <div class="eyebrow">
              <Icon name="activity" :size="14" />The bigger picture
            </div>
            <h1>Incident timeline</h1>
            <p class="muted">
              Understand when pressure started, what changed, and what the
              evidence supports.
            </p>
          </div>
          <span
            class="badge"
            :class="activeIncidents.length ? 'open' : 'online'"
            >{{ activeIncidents.length }} active</span
          >
        </div>
        <div class="filter-row">
          <label
            >System<select v-model="hostFilter">
              <option value="">All systems</option>
              <option v-for="h in hosts" :key="h.id" :value="h.id">
                {{ h.name }}
              </option>
            </select></label
          ><label
            >State<select v-model="stateFilter">
              <option value="">All states</option>
              <option value="open">Open</option>
              <option value="resolved">Resolved</option>
              <option value="interrupted">Interrupted</option>
            </select></label
          ><label
            >Resource<select v-model="resourceFilter">
              <option value="">All resources</option>
              <option value="storage">Storage</option>
              <option value="cpu">CPU</option>
              <option value="memory">Memory</option>
              <option value="lifecycle">Lifecycle</option>
            </select></label
          >
        </div>
        <div class="timeline">
          <IncidentPanel
            v-for="incident in visibleIncidents"
            :key="incident.id"
            :incident="incident"
            :host-name="hostName(incident.host_id)"
            compact
            @inspect="inspect"
          />
          <div v-if="!visibleIncidents.length" class="card empty-state">
            <Icon name="clock" :size="26" />
            <h3>No incidents in this view</h3>
            <p>
              Try another filter, or return when a supported rule detects an
              incident.
            </p>
          </div>
        </div>
      </template>
      <template v-else-if="page === 'incident' && selectedIncident"
        ><div class="breadcrumb">
          <a href="#incidents">Incidents</a
          ><Icon name="chevron" :size="12" /><span>{{
            selectedIncident.title
          }}</span>
        </div>
        <div class="page-heading">
          <div>
            <h1>Incident investigation</h1>
            <p class="muted">
              A record of what was measured, with its limits intact.
            </p>
          </div>
          <a class="button" :href="`#${routeHost(selectedIncident.host_id)}`"
            ><Icon name="server" :size="16" />View system</a
          >
        </div>
        <div class="report-layout">
          <IncidentPanel
            :incident="selectedIncident"
            :host-name="hostName(selectedIncident.host_id)"
          />
          <aside class="card report-sidebar">
            <h3>Incident details</h3>
            <dl>
              <dt>System</dt>
              <dd>{{ hostName(selectedIncident.host_id) }}</dd>
              <dt>Started</dt>
              <dd>{{ date(selectedIncident.start) }}</dd>
              <dt>Last observation</dt>
              <dd>{{ date(selectedIncident.updated) }}</dd>
              <dt>Resource</dt>
              <dd>{{ selectedIncident.resource }}</dd>
              <dt>Rule version</dt>
              <dd>{{ selectedIncident.rule_version }}</dd>
            </dl>
            <div class="notice">
              <Icon name="shield" :size="16" />
              <p>
                Evidence is retained independently of regular metric history.
              </p>
            </div>
            <a
              :href="`/api/v1/incidents/${selectedIncident.id}/report?format=json`"
              class="text-button"
              >Download structured evidence<Icon name="download" :size="14"
            /></a>
          </aside></div
      ></template>

      <template v-else-if="page === 'settings'">
        <div class="page-heading">
          <div>
            <div class="eyebrow">
              <Icon name="settings" :size="14" />Your local workspace
            </div>
            <h1>Settings</h1>
            <p class="muted">
              Manage connected systems, history, and your preferences.
            </p>
          </div>
        </div>
        <div v-if="session?.demo" class="notice">
          <Icon name="info" />
          <p>
            This demonstration uses synthetic telemetry. Settings are read-only;
            appearance preferences remain local to your browser.
          </p>
        </div>
        <div class="settings-grid">
          <section class="card settings-card">
            <div class="settings-title">
              <Icon name="server" />
              <div>
                <h2>Connected systems</h2>
                <p>Enroll agents and review their access.</p>
              </div>
            </div>
            <div v-for="h in hosts" :key="h.id" class="settings-host">
              <span
                ><span class="status-dot" :class="h.status"></span
                >{{ h.name }}</span
              ><span class="muted small">{{
                h.agent_version || "Not enrolled"
              }}</span
              ><button
                class="text-button"
                :disabled="session?.demo || busy"
                @click="enrollExisting(h.id)"
              >
                Enroll
              </button>
            </div>
            <button
              class="button small-button spaced"
              :disabled="session?.demo"
              @click="
                addOpen = true;
                enrollment = null;
              "
            >
              <Icon name="plus" :size="15" />Add system
            </button>
            <div v-if="!session?.demo" class="credential-settings">
              <label
                >Manage agent access<select
                  v-model="managedHost"
                  @change="loadCredentials"
                >
                  <option value="">Select a system</option>
                  <option v-for="h in hosts" :key="h.id" :value="h.id">
                    {{ h.name }}
                  </option>
                </select></label
              >
              <div
                v-for="credential in credentials"
                :key="credential.id"
                class="split"
              >
                <span class="small">Created {{ date(credential.created) }}</span
                ><button
                  class="text-button danger"
                  :disabled="credential.revoked"
                  @click="revoke(credential.id)"
                >
                  {{ credential.revoked ? "Revoked" : "Revoke access" }}
                </button>
              </div>
            </div>
          </section>
          <section class="card settings-card">
            <div class="settings-title">
              <Icon name="clock" />
              <div>
                <h2>Data retention</h2>
                <p>Balance historical detail with local storage.</p>
              </div>
            </div>
            <form
              @submit.prevent="saveRetention"
              class="form-stack retention-form"
            >
              <label
                >Native-resolution history<span
                  ><input
                    v-model.number="retention.raw_hours"
                    type="number"
                    min="1"
                    max="168"
                    :disabled="session?.demo"
                  />hours</span
                ></label
              ><label
                >One-minute history<span
                  ><input
                    v-model.number="retention.minute_days"
                    type="number"
                    min="1"
                    max="90"
                    :disabled="session?.demo"
                  />days</span
                ></label
              ><label
                >Fifteen-minute history<span
                  ><input
                    v-model.number="retention.quarter_days"
                    type="number"
                    min="1"
                    max="365"
                    :disabled="session?.demo"
                  />days</span
                ></label
              ><label
                >Incidents and evidence<span
                  ><input
                    v-model.number="retention.event_days"
                    type="number"
                    min="1"
                    max="365"
                    :disabled="session?.demo"
                  />days</span
                ></label
              ><button
                class="button small-button"
                :disabled="session?.demo || busy"
              >
                Save retention
              </button>
            </form>
          </section>
          <section class="card settings-card">
            <div class="settings-title">
              <Icon name="sun" />
              <div>
                <h2>Preferences</h2>
                <p>These settings apply to this browser.</p>
              </div>
            </div>
            <div class="preference-row">
              <span>Appearance</span>
              <div class="segmented">
                <button
                  :class="{ selected: theme === 'light' }"
                  @click="theme = 'light'"
                >
                  <Icon name="sun" :size="15" />Light</button
                ><button
                  :class="{ selected: theme === 'dark' }"
                  @click="theme = 'dark'"
                >
                  <Icon name="moon" :size="15" />Dark
                </button>
              </div>
            </div>
            <label class="preference-row"
              ><span>Show in-app incident counts</span
              ><input v-model="notify" type="checkbox" class="toggle"
            /></label>
            <div class="notice">
              <Icon name="info" :size="16" />
              <p>
                Sampling and collector permissions are configured on each agent.
                The hub cannot grant itself access to your hosts.
              </p>
            </div>
          </section>
          <section class="card settings-card">
            <div class="settings-title">
              <Icon name="disk" />
              <div>
                <h2>Storage health</h2>
                <p>SQLite on your local filesystem.</p>
              </div>
            </div>
            <dl class="health-list">
              <div>
                <dt>Database</dt>
                <dd>{{ number(health.database_bytes, "bytes") }}</dd>
              </div>
              <div>
                <dt>Write-ahead log</dt>
                <dd>{{ number(health.wal_bytes, "bytes") }}</dd>
              </div>
              <div>
                <dt>Available space</dt>
                <dd>{{ number(health.free_bytes, "bytes") }}</dd>
              </div>
              <div>
                <dt>Ingestion</dt>
                <dd>
                  <span
                    class="badge"
                    :class="health.ingestion_available ? 'online' : 'open'"
                    >{{
                      health.ingestion_available ? "Available" : "Paused"
                    }}</span
                  >
                </dd>
              </div>
            </dl>
            <p class="muted small">
              Ingestion stops below 512 MiB of free space. Back up the database
              with the RootLens backup command.
            </p>
          </section>
        </div>
      </template>
      <div v-else-if="!error" class="empty-state card">
        <h2>This page is unavailable</h2>
        <a href="#overview" class="button">Back to overview</a>
      </div>
      <footer class="footer">
        <span
          ><span class="footer-mark"><Icon name="lens" :size="14" /></span
          >RootLens <span class="separator">·</span> Understand why.</span
        ><span v-if="session?.demo"
          >Synthetic demonstration data · Read-only preview</span
        ><span v-else>Private by design. Evidence by default.</span>
      </footer>
    </main>
  </div>
  <DialogRoot v-model:open="addOpen"
    ><DialogPortal
      ><DialogOverlay class="dialog-overlay" /><DialogContent
        class="dialog-content"
        ><DialogTitle class="dialog-title">{{
          enrollment ? "Connect your agent" : "Add a system"
        }}</DialogTitle
        ><DialogDescription class="muted">{{
          enrollment
            ? "Use this single-use enrollment token on your Linux host. It expires in ten minutes."
            : "Give this system a recognizable name. You’ll then receive an enrollment token for its agent."
        }}</DialogDescription
        ><DialogClose class="dialog-close icon-button" aria-label="Close dialog"
          ><Icon name="close"
        /></DialogClose>
        <form v-if="!enrollment" @submit.prevent="addHost" class="form-stack">
          <label
            >System name<input
              v-model="addName"
              placeholder="homelab-nas"
              required
              maxlength="100" /></label
          ><button class="button primary" :disabled="busy">
            {{ busy ? "Creating…" : "Create system"
            }}<Icon name="arrow" :size="16" />
          </button>
        </form>
        <div v-else class="enrollment-content">
          <label
            >Host identifier<input :value="enrollment.host_id" readonly
          /></label>
          <p class="small muted">
            Save the token as a private file on the agent host, configure the
            hub URL and trusted CA, then start the agent. No host access is
            enabled automatically.
          </p>
          <button class="button primary" @click="downloadToken">
            <Icon name="download" :size="16" />Download enrollment token
          </button>
          <p class="small muted">
            Expires {{ date(enrollment.expires) }}. Treat the file as a
            credential.
          </p>
        </div>
        <p v-if="notice" class="form-error">{{ notice }}</p></DialogContent
      ></DialogPortal
    ></DialogRoot
  >
</template>
