import { createRoot } from "react-dom/client";
import { I18nextProvider } from "react-i18next";
import { Theme } from "@radix-ui/themes";
import { MemoryRouter, Outlet, Route, Routes } from "react-router-dom";
import APIKeysCard from "../src/components/admin/APIKeysCard";
import AdminPanelBar from "../src/components/admin/AdminPanelBar";
import SignOnSettings from "../src/pages/admin/settings/sign-on";
import SettingsLayout from "../src/pages/admin/settings/_layout";
import { AccountContext } from "../src/contexts/AccountContext";
import { AdminNavigationContext } from "../src/contexts/AdminNavigationContext";
import { PublicInfoContext } from "../src/contexts/PublicInfoContext";
import { RPC2Context } from "../src/contexts/RPC2Context";
import { RPC2Client } from "../src/lib/rpc2";
import { RPC2ConnectionState } from "../src/types/rpc2";
import i18n, { initI18n, ensureLanguage } from "../src/i18n/config";
import "@radix-ui/themes/styles.css";
import "../src/global.css";

const params = new URLSearchParams(location.search);
await initI18n();
await ensureLanguage(params.get("language") ?? "en");
await i18n.changeLanguage(params.get("language") ?? "en");
const calls: { method: string; path: string; body: Record<string, unknown> | null }[] = [];
Object.assign(window, { __apiKeyCalls: calls });
type Answer = { status: number; body: unknown };
const answers: Answer[] = [];
let failRead = params.get("listError") === "1";
Object.assign(window, { apiKeyFixture: {
  respond: (answer: Answer) => answers.push(answer),
  failNextRead: () => { failRead = true; },
  changeLanguage: async (language: string) => {
    await ensureLanguage(language);
    await i18n.changeLanguage(language);
  },
} });
let serial = 0;
const scopes = ["nodes:read", "nodes:write", "logs:read", "tasks:read", "tasks:exec", "agents:update", "agents:token:read", "diagnostics:read"];
const keys = [{
  id: "00000000-0000-4000-8000-000000000001", name: "Existing monitor", scopes: ["nodes:read"],
  created_at: "2026-01-01T00:00:00Z", expires_at: "2099-01-01T00:00:00Z", revoked_at: null as string | null,
}];
const longList = params.get("layout") === "1";
if (longList) {
  for (let index = 0; index < 12; index++) keys.push({
    id: `00000000-0000-4000-8001-${String(index + 1).padStart(12, "0")}`,
    name: `Existing seed ${index}`, scopes: ["nodes:read"],
    created_at: "2026-01-01T00:00:00Z", expires_at: "2099-01-01T00:00:00Z",
    revoked_at: index % 2 ? "2026-01-02T00:00:00Z" : null,
  });
}
const realFetch = globalThis.fetch.bind(globalThis);
globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
  const path = String(input);
  const json = (body: unknown) => new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
  if (longList) {
    if (path === "/api/admin/settings") return json({ status: "success", data: { o_auth_provider: "fixture" } });
    if (path.startsWith("/api/admin/settings/oidc")) return json({ status: "success", data: path.includes("?provider=")
      ? { addition: "{}" } : { fixture: [] } });
    if (path === "/api/rpc2") {
      const rpc = JSON.parse(String(init?.body));
      return json({ jsonrpc: "2.0", id: rpc.id, result: rpc.method === "common:getVersion"
        ? { version: "", hash: "fixture" } : [] });
    }
  }
  if (!path.startsWith("/api/admin/api-keys")) return realFetch(input, init);
  const body = init?.body ? JSON.parse(String(init.body)) : null;
  calls.push({ method: init?.method ?? "GET", path, body });
  const answer = (data: unknown, status = "success") => new Response(JSON.stringify({ status, data, message: status === "error" ? "Scope approval refused" : "" }), {
    status: 200, headers: { "Content-Type": "application/json" },
  });
  if (init?.method !== "POST") {
    if (failRead) {
      failRead = false;
      return new Response(JSON.stringify({ status: "error", message: "Failed to list API keys", error_key: "api_keys_unavailable" }), {
        status: 500, headers: { "Content-Type": "application/json" },
      });
    }
    return answer({ keys, scopes });
  }
  const planned = answers.shift();
  if (planned) return new Response(JSON.stringify(planned.body), {
    status: planned.status, headers: { "Content-Type": "application/json" },
  });
  if (params.get("refuse") === "1") return answer(null, "error");
  if (path.endsWith("/revoke")) {
    const key = keys.find((key) => path.includes(key.id));
    if (key) key.revoked_at = new Date().toISOString();
    return answer(null);
  }
  if (path.endsWith("/rotate")) {
    return answer({ key: keys.find((key) => path.includes(key.id)), token: `nk_fixture-rotated-${++serial}` });
  }
  const key = {
    id: `00000000-0000-4000-8000-${String(++serial + 1).padStart(12, "0")}`,
    name: String(body.name), scopes: body.scopes as string[], created_at: "2026-01-01T00:00:00Z", expires_at: "2099-01-01T00:00:00Z", revoked_at: null,
  };
  keys.push(key);
  return answer({ key, token: `nk_fixture-created-${serial}` });
}) as typeof fetch;

const account = {
  account: { logged_in: true, username: "admin", uuid: "user-1", sso_id: "", sso_type: "", "2fa_enabled": params.get("tfa") === "1" },
  loading: false, error: null, refresh: () => {},
};
const client = new RPC2Client("/api/rpc2", { autoConnect: false });
createRoot(document.getElementById("root")!).render(
  <I18nextProvider i18n={i18n}>
    <Theme scaling={longList ? "110%" : "100%"}><AccountContext.Provider value={account}>
      {longList ? <MemoryRouter initialEntries={["/admin/settings/sign-on"]}>
        <RPC2Context.Provider value={{ client, connectionState: RPC2ConnectionState.DISCONNECTED,
          isConnected: false, error: null, connect: async () => {}, disconnect: () => {} }}>
          <PublicInfoContext.Provider value={{ publicInfo: null, isLoading: false, error: null, refresh: async () => {} }}>
            <AdminNavigationContext.Provider value={{ refreshVersion: 0, refreshNavigation: () => {} }}>
              <Routes><Route element={<AdminPanelBar content={<Outlet />} />}>
                <Route element={<SettingsLayout />}><Route path="/admin/settings/sign-on" element={<SignOnSettings />} /></Route>
              </Route></Routes>
            </AdminNavigationContext.Provider>
          </PublicInfoContext.Provider>
        </RPC2Context.Provider>
      </MemoryRouter> : <div data-testid="api-keys-ready"><APIKeysCard /></div>}
    </AccountContext.Provider></Theme>
  </I18nextProvider>,
);
