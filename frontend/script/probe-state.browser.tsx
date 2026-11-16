import { createRoot } from "react-dom/client";
import i18next from "i18next";
import { I18nextProvider } from "react-i18next";
import { Theme } from "@radix-ui/themes";
import { RPC2Context, type RPC2ContextType } from "../src/contexts/RPC2Context";
import ProbeQualityPage from "../src/pages/admin/probe-quality";
import { ForecastShell } from "../src/pages/forecast";
import en from "../src/i18n/locales/en.json";
import zh from "../src/i18n/locales/zh_CN.json";
import "@radix-ui/themes/styles.css";

const params = new URLSearchParams(location.search);
const i18n = i18next.createInstance();
await i18n.init({ lng: params.get("lang") || "en", fallbackLng: "en", resources: { en: { translation: en }, zh_CN: { translation: zh } }, interpolation: { escapeValue: false } });
const mode = params.get("page") || "quality";
const state = { failNext: params.get("initialFailure") === "1", calls: 0 };
Object.assign(window, { probeFixture: state });
const client = {
  call: async (method: string) => {
    state.calls++;
    if (state.failNext) { state.failNext = false; throw new Error("fixture read failed"); }
    if (method === "admin:getProbeQuality") return {
      now: "2026-11-15T12:00:00Z", nodes: [{ uuid: "one", name: "Fixture node", last_report_age_seconds: 10,
        quality: { cpu: "unknown" }, quality_fields: 1, unknown_fields: 1, cgroup_present: false, disk_mounts: 0,
        tcp_counters_present: false, boot_id_hash_present: false }],
    };
    if (method === "admin:getTrafficForecast") return { rows: [], cycle: { start: "2026-11-01", end: "2026-12-01", reset_day: 1, location: "UTC" }, threshold: 0.9 };
    throw new Error(`Unexpected call: ${method}`);
  },
} as unknown as RPC2ContextType["client"];
const context: RPC2ContextType = { client, connectionState: "connected" as RPC2ContextType["connectionState"], isConnected: true, error: null, connect: async () => {}, disconnect: () => {} };
createRoot(document.getElementById("root")!).render(<I18nextProvider i18n={i18n}><Theme><RPC2Context.Provider value={context}>{mode === "quality" ? <ProbeQualityPage /> : <ForecastShell />}</RPC2Context.Provider></Theme></I18nextProvider>);
