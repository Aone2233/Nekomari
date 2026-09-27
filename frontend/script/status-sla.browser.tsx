import { createRoot } from "react-dom/client";
import i18next from "i18next";
import { I18nextProvider } from "react-i18next";
import { Theme } from "@radix-ui/themes";
import { SlaReportTable } from "../src/components/status/SlaReportTable";
import type { SlaReport } from "../src/types/Sla";
import "@radix-ui/themes/styles.css";

/**
 * Mounted fixture for the SLA report table (roadmap H1).
 *
 * The table takes its report as a prop, so every state the page can be in is
 * renderable here without a server: loading, an error, an empty window, a node that
 * reported nothing, a node with an outage, and a node whose *target* failed while the
 * node itself was up.
 *
 * The state comes from `?state=` so one page serves all of them. The assertions read
 * DOM attributes and visible text, and the resources below are English only — what is
 * being tested is that a figure is not shown where there is no evidence for it, not
 * which language it is in.
 *
 * Built a minute at a time at a fixed instant rather than "now", so the incident times
 * the spec asserts on are stable.
 */

const i18n = i18next.createInstance();
await i18n.init({
  lng: "en",
  resources: {
    en: {
      translation: {
        "status.title": "Status and SLA",
        "status.subtitle": "Availability over the selected window.",
        "status.window": "Window",
        "status.window_24h": "24 hours",
        "status.window_7d": "7 days",
        "status.window_30d": "30 days",
        "status.window_90d": "90 days",
        "status.loading": "Loading the report…",
        "status.error": "Could not load the report",
        "status.empty": "No nodes reported in this window.",
        "status.noData": "no data",
        "status.coverage": "Coverage",
        "status.coverageOf": "{{percent}} ({{observed}}/{{expected}} samples)",
        "status.availability": "Availability",
        "status.latency": "Latency p50/p95/p99 (ms)",
        "status.latencyValues": "{{p50}} / {{p95}} / {{p99}}",
        "status.outages": "Outages",
        "status.noOutages": "none",
        "status.reportingGaps": "Reporting gaps (not outages — the node stopped reporting)",
        "status.task": "Task",
        "status.untaggedSeries": "untagged series",
        "status.nodeNoData": "This node reported nothing in this window.",
        "status.sampleInterval": "Buckets of {{seconds}}s",
        "status.clamped": "Shortened to the retained range",
      },
    },
  },
});

const BASE = "2026-09-26T12:00:00Z";
const at = (minute: number): string =>
  new Date(Date.parse(BASE) + minute * 60_000).toISOString();

const presence = (observed: number, expected: number) => ({
  expected_buckets: expected,
  observed_buckets: observed,
  coverage: expected ? observed / expected : 0,
  first_data: observed ? at(0) : "",
  last_data: observed ? at(observed - 1) : "",
});

/**
 * healthy: one node, two tasks, everything measured and up.
 *
 * Its presence is 60/60 so the coverage badge reads 100%, which is the case that must be
 * distinguishable from the no-data node below.
 */
const healthy: SlaReport = {
  start: at(0),
  end: at(60),
  window: "24h",
  interval_seconds: 60,
  count: 1,
  window_presets: { "24h": 24, "7d": 168, "30d": 720, "90d": 2160 },
  nodes: [
    {
      entity_id: "node-a",
      presence: presence(60, 60),
      has_report: true,
      reporting_gaps: [],
      tasks: [
        {
          task_id: "1",
          loss: { fraction: 1, has_data: true, buckets: 60, lost: 0, presence: presence(60, 60), window: 0 },
          latency: { p50_ms: 12.5, p95_ms: 30.2, p99_ms: 48.9, buckets: 60, has_data: true },
          outages: [],
        },
        {
          task_id: "2",
          loss: { fraction: 0.9875, has_data: true, buckets: 60, lost: 1, presence: presence(60, 60), window: 0 },
          latency: { p50_ms: 8.1, p95_ms: 19.4, p99_ms: 22.0, buckets: 60, has_data: true },
          outages: [],
        },
      ],
    },
  ],
};

/**
 * withOutage: one node, a target unreachable from minute 20 to 24 while the node itself
 * kept reporting.
 *
 * This is the distinction the whole report exists to keep: the node's own presence is
 * untouched, and the outage belongs to the task.
 */
const withOutage: SlaReport = {
  start: at(0),
  end: at(60),
  window: "24h",
  interval_seconds: 60,
  count: 1,
  window_presets: { "24h": 24, "7d": 168, "30d": 720, "90d": 2160 },
  nodes: [
    {
      entity_id: "node-a",
      presence: presence(60, 60),
      has_report: true,
      reporting_gaps: [],
      tasks: [
        {
          task_id: "7",
          loss: { fraction: 55 / 60, has_data: true, buckets: 60, lost: 5, presence: presence(60, 60), window: 0 },
          latency: { p50_ms: 15.0, p95_ms: 40.0, p99_ms: 60.0, buckets: 55, has_data: true },
          outages: [
            { Start: at(20), End: at(24), buckets: 5, duration: 240_000_000_000, peak_loss: 1 },
          ],
        },
      ],
    },
  ],
};

/**
 * noData: a node that reported nothing at all.
 *
 * `has_data` is false and the fraction is 0. This is the case that must NOT render as
 * "0.0%" — a dead node and an unmeasured one would otherwise look the same, and
 * unmeasured is what an offline node looks like here.
 */
const noData: SlaReport = {
  start: at(0),
  end: at(60),
  window: "24h",
  interval_seconds: 60,
  count: 1,
  window_presets: { "24h": 24, "7d": 168, "30d": 720, "90d": 2160 },
  nodes: [
    {
      entity_id: "node-silent",
      presence: presence(0, 0),
      has_report: false,
      reporting_gaps: [],
      tasks: [
        {
          task_id: "1",
          loss: { fraction: 0, has_data: false, buckets: 0, lost: 0, presence: presence(0, 0), window: 0 },
          latency: { p50_ms: 0, p95_ms: 0, p99_ms: 0, buckets: 0, has_data: false },
          outages: [],
        },
      ],
    },
  ],
};

/**
 * gapsAndClamp: one node that stopped reporting for a stretch, plus a window the server
 * shortened because the store does not retain that far back.
 *
 * The gaps are reported as gaps, in their own section, and are deliberately not folded
 * into an availability figure — the node was not measured, which is not the same as
 * being down.
 */
const gapsAndClamp: SlaReport = {
  start: at(0),
  end: at(60),
  window: "90d",
  interval_seconds: 3600,
  clamped: "requested 90h0m0s but only 30h0m0s is retained",
  count: 1,
  window_presets: { "24h": 24, "7d": 168, "30d": 720, "90d": 2160 },
  nodes: [
    {
      entity_id: "node-a",
      presence: presence(40, 60),
      has_report: true,
      reporting_gaps: [
        { Start: at(30), End: at(49), buckets: 20, duration: 1_140_000_000_000, peak_loss: 1 },
      ],
      tasks: [
        {
          task_id: "1",
          loss: { fraction: 1, has_data: true, buckets: 40, lost: 0, presence: presence(40, 60), window: 0 },
          latency: { p50_ms: 10.0, p95_ms: 25.0, p99_ms: 30.0, buckets: 40, has_data: true },
          outages: [],
        },
      ],
    },
  ],
};

/** hiddenAbsent: a report that simply does not mention a hidden node. */
const hiddenAbsent: SlaReport = {
  start: at(0),
  end: at(60),
  window: "24h",
  interval_seconds: 60,
  count: 1,
  window_presets: { "24h": 24, "7d": 168, "30d": 720, "90d": 2160 },
  nodes: [healthy.nodes[0]],
};

const REPORTS: Record<string, SlaReport> = {
  healthy,
  outage: withOutage,
  nodata: noData,
  gaps: gapsAndClamp,
  hidden: hiddenAbsent,
};

const params = new URLSearchParams(globalThis.location.search);
const state = params.get("state") ?? "healthy";

const root = createRoot(document.getElementById("root")!);

const render = (props: Parameters<typeof SlaReportTable>[0]) =>
  root.render(
    <I18nextProvider i18n={i18n}>
      <Theme appearance="dark">
        <SlaReportTable {...props} />
      </Theme>
    </I18nextProvider>,
  );

const NODE_NAMES: Record<string, string> = {
  "node-a": "Tokyo Relay",
  "node-silent": "Silent Node",
};
const TASK_NAMES: Record<string, string> = { "1": "Cloudflare TCP 443", "2": "NOSLA relay", "7": "Beijing CERNET" };

if (state === "error") {
  render({ report: null, error: "metric store not initialized", window: "24h" });
} else {
  render({
    report: REPORTS[state] ?? healthy,
    window: (REPORTS[state]?.window as "24h") ?? "24h",
    nodeNames: NODE_NAMES,
    taskNames: TASK_NAMES,
  });
}

// The loading state has to be able to change to a report without a reload, which is what
// the spec asserts on: a page stuck on "loading" after the data arrived looks exactly
// like a slow one.
declare global {
  interface Window {
    __showState?: (name: string) => void;
  }
}
globalThis.__showState = (name: string) => {
  if (name === "loading") {
    render({ report: null, loading: true, window: "24h", nodeNames: NODE_NAMES, taskNames: TASK_NAMES });
    return;
  }
  render({
    report: REPORTS[name] ?? healthy,
    window: (REPORTS[name]?.window as "24h") ?? "24h",
    nodeNames: NODE_NAMES,
    taskNames: TASK_NAMES,
  });
};

if (state === "loading") {
  globalThis.__showState("loading");
}
