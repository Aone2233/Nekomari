import { createRoot } from "react-dom/client";
import i18next from "i18next";
import { I18nextProvider } from "react-i18next";
import { Theme } from "@radix-ui/themes";
import { MemoryRouter, Route, Routes, useNavigate } from "react-router-dom";
import { AccountContext, type Account } from "../src/contexts/AccountContext";
import {
  NodeListContext,
  type NodeBasicInfo,
} from "../src/contexts/NodeListContext";
import {
  PublicInfoContext,
  type PublicInfo,
} from "../src/contexts/PublicInfoContext";
import { RPC2Context } from "../src/contexts/RPC2Context";
import type { RPC2Client } from "../src/lib/rpc2";
import LoadChart from "../src/pages/instance/LoadChart";
import "@radix-ui/themes/styles.css";
import "../src/global.css";

const i18n = i18next.createInstance();
await i18n.init({
  lng: "en",
  resources: {
    en: {
      translation: {
        "common.real_time": "Real-time",
        "chart.minutes": "{{count}} min",
        "chart.hours": "{{count}} h",
        "chart.days": "{{count}} d",
        "chart.customRange": "Custom range",
        "chart.samplingAlgorithm": "Sampling algorithm",
        "chart.avg": "Average",
        "chart.sampling.min": "Min",
        "chart.sampling.max": "Max",
        "chart.sampling.first": "First",
        "chart.sampling.last": "Last",
        "chart.sampling.stddev": "Stddev",
        "chart.sampling.p70": "P70",
        "chart.sampling.p95": "P95",
        "chart.sampling.p99": "P99",
      },
    },
  },
});

// One queued entry per RPC call. A held entry parks the call until the spec
// resolves it, which is how an older response is kept in flight on purpose.
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

const fallbacks: Record<string, unknown> = {
  "public:listMetricDefinitions": [],
  "public:getPublicPingTasks": [],
};

const seeded = (window as any).__loadChartSeed;
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

const account: Account = {
  logged_in: false,
  sso_id: "",
  sso_type: "",
  username: "",
  uuid: "",
  "2fa_enabled": false,
};

const publicInfo: PublicInfo = {
  cors_origin_check_enabled: false,
  custom_body: "",
  custom_head: "",
  description: "",
  disable_password_login: false,
  oauth_provider: "",
  oauth_enable: false,
  metric_retention_days: 7,
  sitename: "fixture",
  private_site: false,
  theme: "default",
  theme_settings: {
    chartDashboardTemplate: JSON.stringify([
      { id: "traffic", title: "Traffic", metrics: ["traffic.up"], size: "small" },
    ]),
  },
};

const node: NodeBasicInfo = {
  uuid: "node-1",
  name: "alpha",
  cpu_name: "",
  virtualization: "",
  arch: "",
  cpu_cores: 1,
  os: "",
  kernel_version: "",
  gpu_name: "",
  region: "",
  mem_total: 0,
  swap_total: 0,
  disk_total: 0,
  version: "",
  weight: 0,
  price: 0,
  tags: "",
  billing_cycle: 0,
  currency: "",
  group: "",
  traffic_limit: 0,
  traffic_limit_type: undefined,
  expired_at: "",
  created_at: "",
  updated_at: "",
};

export function FixtureRoutes() {
  const navigate = useNavigate();
  return (
    <>
      <button type="button" onClick={() => navigate("/away")}>
        Leave instance
      </button>
      <button type="button" onClick={() => navigate("/node/node-1")}>
        Back to instance
      </button>
      <Routes>
        <Route path="/node/:uuid" element={<LoadChart />} />
        <Route
          path="/away"
          element={<div data-testid="away">away from the instance</div>}
        />
      </Routes>
    </>
  );
}

Object.assign(window, {
  loadChartFixture: {
    calls: () => calls.map((call) => ({ method: call.method, params: call.params })),
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
  },
});

createRoot(document.getElementById("root")!).render(
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
        <AccountContext.Provider
          value={{ account, loading: false, error: null, refresh: () => {} }}
        >
          <PublicInfoContext.Provider
            value={{
              publicInfo,
              isLoading: false,
              error: null,
              refresh: async () => {},
            }}
          >
            <NodeListContext.Provider
              value={{ nodeList: [node], isLoading: false, error: null, refresh: () => {} }}
            >
              <MemoryRouter initialEntries={["/node/node-1"]}>
                <FixtureRoutes />
              </MemoryRouter>
            </NodeListContext.Provider>
          </PublicInfoContext.Provider>
        </AccountContext.Provider>
      </RPC2Context.Provider>
    </Theme>
  </I18nextProvider>,
);
