import { useSyncExternalStore } from "react";
import { createRoot } from "react-dom/client";
import i18next from "i18next";
import { I18nextProvider } from "react-i18next";
import { Theme } from "@radix-ui/themes";
import { RPC2Context } from "../src/contexts/RPC2Context";
import type { RPC2Client } from "../src/lib/rpc2";
import MetricsSettings from "../src/pages/admin/settings/metrics";
import "@radix-ui/themes/styles.css";
import "../src/global.css";

const i18n = i18next.createInstance();
await i18n.init({
  lng: "en",
  resources: {
    en: {
      translation: {
        "settings.metrics.title": "Metrics",
        "settings.metrics.loading_metrics": "Loading metrics",
        "settings.metrics.no_metrics": "No metrics",
        "settings.metrics.retention_title": "Retention",
        "settings.metrics.migration_card_title": "Migration",
      },
    },
    zh: {
      translation: {
        "settings.metrics.title": "指标",
        "settings.metrics.loading_metrics": "正在加载指标",
        "settings.metrics.no_metrics": "没有指标",
        "settings.metrics.retention_title": "保留策略",
        "settings.metrics.migration_card_title": "迁移",
      },
    },
  },
});

type Queued = { hold?: string; value?: unknown };
type Pending = { resolve: (value: unknown) => void; reject: (error: Error) => void };

const calls: Array<{ method: string; params: Record<string, unknown> }> = [];
const queues = new Map<string, Queued[]>();
const pending = new Map<string, Pending>();

const pushQueue = (method: string, entry: Queued) => {
  const queue = queues.get(method) ?? [];
  queue.push(entry);
  queues.set(method, queue);
};

const idleMigrationStatus = {
  status: "idle",
  is_running: false,
  source_driver: "",
  source_dsn: "",
  target_driver: "",
  target_dsn: "",
  total_metrics: 0,
  metrics_done: 0,
  current_metric: "",
  migrated_points: 0,
};

const fallbacks: Record<string, unknown> = {
  "admin:getMetricMigrationStatus": idleMigrationStatus,
};

const seeded = (window as any).__metricsSeed;
if (Array.isArray(seeded)) {
  for (const entry of seeded as Array<Queued & { method: string }>) {
    pushQueue(entry.method, entry);
  }
}

const client = {
  call: (method: string, params?: Record<string, unknown>) => {
    calls.push({ method, params: { ...(params ?? {}) } });
    const queued = queues.get(method)?.shift();
    if (!queued) {
      if (method in fallbacks) return Promise.resolve(fallbacks[method]);
      return Promise.reject(new Error(`Unexpected RPC: ${method}`));
    }
    if (queued.hold) {
      const key = queued.hold;
      return new Promise((resolve, reject) => {
        pending.set(key, { resolve, reject });
      });
    }
    return Promise.resolve(queued.value);
  },
} as unknown as RPC2Client;

const nativeFetch = window.fetch.bind(window);
const json = (body: unknown) =>
  new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });

const settingsPayload = {
  status: "success",
  message: "ok",
  data: {
    sitename: "fixture",
    description: "",
    metric_retention_days: 1,
    metric_db_dsn: "",
    metric_table_prefix: "metric_",
    metric_max_open_conns: 25,
    metric_max_idle_conns: 5,
    metric_rollup_minute_retention_minutes: 600,
    metric_rollup_five_minute_retention_minutes: 3000,
    metric_rollup_hour_retention_hours: 600,
  },
};

const databaseOverview = {
  status: "success",
  message: "ok",
  data: {
    main: { driver: "sqlite", location: "local", size: 1024, action: "vacuum" },
    monitoring: {
      driver: "sqlite",
      location: "local",
      size: 2048,
      action: "vacuum",
    },
    local_total: 3072,
  },
};

window.fetch = (input, init) => {
  const url = new URL(String(input), window.location.href);
  if (url.pathname === "/api/admin/settings") {
    return Promise.resolve(json(settingsPayload));
  }
  if (url.pathname === "/api/admin/database/size") {
    return Promise.resolve(json(databaseOverview));
  }
  return nativeFetch(input, init);
};

let fixtureState = { mounted: true };
const listeners = new Set<() => void>();
const subscribe = (listener: () => void) => {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
};
const getSnapshot = () => fixtureState;
const updateFixture = (patch: Partial<typeof fixtureState>) => {
  fixtureState = { ...fixtureState, ...patch };
  for (const listener of listeners) listener();
};

export function Fixture() {
  const { mounted } = useSyncExternalStore(subscribe, getSnapshot);
  return (
    <I18nextProvider i18n={i18n}>
      <Theme>
        <RPC2Context.Provider
          value={{
            client,
            connectionState: "connected",
            isConnected: true,
            error: null,
            connect: async () => {},
            disconnect: () => {},
          }}
        >
          <button type="button" onClick={() => updateFixture({ mounted: false })}>
            Leave settings
          </button>
          <button type="button" onClick={() => updateFixture({ mounted: true })}>
            Back to settings
          </button>
          {mounted ? (
            <MetricsSettings />
          ) : (
            <div data-testid="away">away from the settings page</div>
          )}
        </RPC2Context.Provider>
      </Theme>
    </I18nextProvider>
  );
}

Object.assign(window, {
  metricsFixture: {
    requests: (method: string) =>
      calls.filter((call) => call.method === method).length,
    respond: (method: string, value: unknown) => pushQueue(method, { value }),
    pending: (key: string) => pending.has(key),
    resolve: (key: string, value: unknown) => {
      const entry = pending.get(key);
      if (!entry) throw new Error(`No pending response: ${key}`);
      pending.delete(key);
      entry.resolve(value);
    },
    setLanguage: (language: string) => i18n.changeLanguage(language),
  },
});

createRoot(document.getElementById("root")!).render(<Fixture />);
