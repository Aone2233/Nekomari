import { createRoot } from "react-dom/client";
import i18next from "i18next";
import { I18nextProvider } from "react-i18next";
import { Theme } from "@radix-ui/themes";
import Dashboard from "../src/pages/admin/dashboard";
import { NodeListContext, type NodeBasicInfo } from "../src/contexts/NodeListContext";
import { RPC2Context, type RPC2ContextType } from "../src/contexts/RPC2Context";
import { DAY_MS } from "../src/pages/admin/expiry";

const start = Date.UTC(2026, 8, 23, 12);
const node = (uuid: string, offset: number) => ({
  uuid,
  name: uuid,
  billing_cycle: 30,
  expired_at: new Date(start + offset).toISOString(),
}) as NodeBasicInfo;
const nodeList = [
  node("outside", 7 * DAY_MS + 30_000),
  node("three", 3 * DAY_MS + 30_000),
  node("expiring", 30_000),
];
let refreshCount = 0;
let rpcCount = 0;
const rpc = {
  isConnected: false,
  client: {
    call: async (method: string) => {
      rpcCount++;
      switch (method) {
        case "common:getNodesLatestStatus": return {};
        case "public:queryMetrics": return { series: [] };
        case "public:getPingMetricStats": return { stats: [] };
        case "public:getPublicPingTasks": return [];
        default: throw new Error(`Unexpected RPC: ${method}`);
      }
    },
  },
} as RPC2ContextType;

const i18n = i18next.createInstance();
await i18n.init({
  lng: "en",
  resources: { en: { translation: {
    "dashboard.expiringSoon": "Expiring soon",
    "dashboard.daysLeft": "{{days}} days left",
    "dashboard.noExpiring": "No servers expiring soon",
  } } },
});

const root = createRoot(document.getElementById("root")!);
root.render(
  <I18nextProvider i18n={i18n}>
    <Theme>
      <NodeListContext.Provider value={{
        nodeList, isLoading: false, error: null,
        refresh: () => { refreshCount++; },
      }}>
        <RPC2Context.Provider value={rpc}>
          <Dashboard />
        </RPC2Context.Provider>
      </NodeListContext.Provider>
    </Theme>
  </I18nextProvider>,
);
Object.assign(window, {
  unmountDashboardClockFixture: () => root.unmount(),
  dashboardRequestCounts: () => ({ refreshCount, rpcCount }),
});
