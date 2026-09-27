import { createRoot } from "react-dom/client";
import i18next from "i18next";
import { I18nextProvider } from "react-i18next";
import { Theme } from "@radix-ui/themes";
import {
  TrafficForecastPage,
  type ForecastRow,
  type ForecastCycle,
} from "../src/components/forecast/TrafficForecastPage";
import "@radix-ui/themes/styles.css";

/**
 * Mounted fixture for the traffic forecast page (roadmap H5).
 *
 * `?state=ready` puts all three kinds of row on screen at once, which is the assertion that matters: a
 * node projected to exceed its limit, a node projected to be fine, and a node the server refused to
 * project. The refusal is the one worth pinning — `insufficient` and "no problem" both produce no number,
 * and the whole point of the page is that they must not look alike.
 *
 * `?state=refused` makes the cycle-day write fail, so the error path is reachable too.
 */

const i18n = i18next.createInstance();
await i18n.init({
  lng: "en",
  resources: {
    en: {
      translation: {
        "forecast.title": "Traffic forecast",
        "forecast.subtitle": "Where each node's traffic is heading this cycle.",
        "forecast.cycleWindow": "This cycle",
        "forecast.cycleDay": "Cycle day",
        "forecast.timezone": "Computed in",
        "forecast.threshold": "warns at {{percent}}%",
        "forecast.preview": "Preview",
        "forecast.saveDay": "Save day",
        "forecast.error": "Could not apply",
        "forecast.exceeding": "{{count}} projected over the limit",
        "forecast.projected": "{{count}} projected",
        "forecast.refused": "{{count}} with too little data",
        "forecast.loading": "Loading projections…",
        "forecast.node": "Node",
        "forecast.used": "Used",
        "forecast.projectedEnd": "Projected end",
        "forecast.limit": "Limit",
        "forecast.crosses": "Limit reached in",
        "forecast.basis": "Basis",
        "forecast.noLimit": "no limit",
        "forecast.insufficient": "not enough data",
        "forecast.inDays": "{{days}} days",
        "forecast.notThisCycle": "not this cycle",
        "forecast.basisDetail": "{{samples}} samples, {{coverage}}% of cycle, ±{{uncertainty}}%",
      },
    },
  },
});

const GiB = 1024 ** 3;
const basis = { samples: 41, observed_for: 0, coverage: 1 / 3, bytes_per_day: 10 * GiB, uncertainty: 0.07, cycle_days: 30 };

const ROWS: ForecastRow[] = [
  {
    uuid: "uuid-over",
    name: "Tokyo Relay",
    method: "linear_rate",
    used_bytes: 100 * GiB,
    projected_bytes: 300 * GiB,
    limit_bytes: 250 * GiB,
    limit_type: "sum",
    limit_set: true,
    projected_fraction: 1.2,
    would_exceed: true,
    crosses_at: "2026-10-05T00:00:00Z",
    crosses_in_days: 5,
    crosses_in_cycle: true,
    basis,
    warning: { threshold: 0.9, projected_fraction: 1.2, message: "projected 300 GiB" },
  },
  {
    uuid: "uuid-fine",
    name: "Frankfurt Box",
    method: "linear_rate",
    used_bytes: 20 * GiB,
    projected_bytes: 60 * GiB,
    limit_bytes: 500 * GiB,
    limit_type: "max",
    limit_set: true,
    projected_fraction: 0.12,
    would_exceed: false,
    crosses_at: "2026-12-01T00:00:00Z",
    crosses_in_days: 65,
    crosses_in_cycle: false,
    basis,
  },
  {
    uuid: "uuid-young",
    name: "Silent Node",
    method: "insufficient",
    reason: "only 2 observation(s) so far; a projection needs at least 5",
    used_bytes: GiB,
    projected_bytes: 0,
    limit_bytes: 100 * GiB,
    limit_type: "sum",
    limit_set: true,
    projected_fraction: 0,
    would_exceed: false,
    crosses_in_cycle: false,
    basis: { ...basis, samples: 2, coverage: 0.01, bytes_per_day: 0, uncertainty: 0 },
  },
  {
    uuid: "uuid-nolimit",
    name: "Home Lab",
    method: "linear_rate",
    used_bytes: 5 * GiB,
    projected_bytes: 15 * GiB,
    limit_bytes: 0,
    limit_type: "",
    limit_set: false,
    projected_fraction: 0,
    would_exceed: false,
    crosses_in_cycle: false,
    basis,
  },
];

const CYCLE: ForecastCycle = {
  start: "2026-09-01T00:00:00Z",
  end: "2026-10-01T00:00:00Z",
  reset_day: 1,
  location: "UTC",
};

const params = new URLSearchParams(globalThis.location.search);
const state = params.get("state") ?? "ready";

declare global {
  interface Window {
    __forecastCalls?: { preview: number[]; save: number[] };
  }
}
globalThis.__forecastCalls = { preview: [], save: [] };

const onPreview = async (day: number) => {
  globalThis.__forecastCalls!.preview.push(day);
};

const onSetCycleDay = async (day: number) => {
  globalThis.__forecastCalls!.save.push(day);
  if (state === "refused") {
    throw new Error("reset_day must be between 1 and 31");
  }
};

createRoot(document.getElementById("root")!).render(
  <I18nextProvider i18n={i18n}>
    <Theme appearance="dark">
      <TrafficForecastPage
        rows={state === "loading" ? [] : ROWS}
        cycle={CYCLE}
        threshold={0.9}
        onPreview={onPreview}
        onSetCycleDay={onSetCycleDay}
        loading={state === "loading"}
      />
    </Theme>
  </I18nextProvider>,
);