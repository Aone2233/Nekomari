import { createRoot } from "react-dom/client";
import { I18nextProvider } from "react-i18next";
import { Theme } from "@radix-ui/themes";
import { RotateTokenButton } from "../src/pages/admin/nodeTable/RotateTokenButton";
import { AccountContext } from "../src/contexts/AccountContext";
import { NodeDetailsContext, type NodeDetail } from "../src/contexts/NodeDetailsContext";
import i18n, { initI18n, ensureLanguage } from "../src/i18n/config";
import "@radix-ui/themes/styles.css";
import "../src/global.css";

const params = new URLSearchParams(location.search);
await initI18n();
await ensureLanguage(params.get("language") ?? "en");
await i18n.changeLanguage(params.get("language") ?? "en");
const calls: { path: string; body: unknown }[] = [];
const state = { refreshes: 0 };
let release: (() => void) | undefined;
Object.assign(window, { clientTokenFixture: { calls, state, release: () => release?.() } });
const realFetch = globalThis.fetch.bind(globalThis);
globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
  const path = String(input);
  if (!path.includes("/token/rotate")) return realFetch(input, init);
  calls.push({ path, body: JSON.parse(String(init?.body)) });
  if (params.get("hold") === "1") await new Promise<void>((resolve) => { release = resolve; });
  if (params.get("fail") === "1") throw new TypeError("fixture response lost after commit");
  return new Response(JSON.stringify({ status: params.get("refuse") === "1" ? "error" : "success", token: "na_fixture-only-token" }), {
    status: 200, headers: { "Content-Type": "application/json", "Cache-Control": "no-store" },
  });
}) as typeof fetch;
const node = { uuid: "00000000-0000-4000-8000-000000000001", name: "Test node" } as NodeDetail;
createRoot(document.getElementById("root")!).render(
  <I18nextProvider i18n={i18n}>
    <Theme appearance={params.get("dark") === "1" ? "dark" : "light"}>
      <AccountContext.Provider value={{ account: { logged_in: true, username: "fixture", uuid: "fixture", sso_id: "", sso_type: "", "2fa_enabled": params.get("tfa") === "1" }, loading: false, error: null, refresh: () => {} }}>
        <NodeDetailsContext.Provider value={{ nodeDetail: [node], isLoading: false, error: null, refresh: () => { state.refreshes++; } }}>
          <RotateTokenButton node={node} />
        </NodeDetailsContext.Provider>
      </AccountContext.Provider>
    </Theme>
  </I18nextProvider>,
);
