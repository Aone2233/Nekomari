import { createRoot } from "react-dom/client";
import { useState } from "react";
import { BrowserRouter } from "react-router-dom";
import i18next from "i18next";
import { I18nextProvider } from "react-i18next";
import { Theme } from "@radix-ui/themes";
import { Toaster } from "sonner";
import { RPC2Provider } from "../src/contexts/RPC2Provider";
import { AutoDiscoverySection } from "../src/pages/admin/autoDiscovery/AutoDiscoverySection";
import "@radix-ui/themes/styles.css";

// Mounts `AutoDiscoverySection` on its own. The page-level fixture
// (`admin-node-table.browser.tsx`) reaches it through the "add node" dialog, but
// only in its disabled state — the dialog has to be opened and `settings` has to
// carry an auto-discovery key before the interesting half renders. This fixture
// drives the section directly, so its two other branches and the command it
// builds are under test without depending on the dialog.
//
// That matters because the section builds its own copy of the install command.
// It is a separate implementation from `nodeTable/GenerateCommandButton.tsx`, and
// the two have to agree about flags, quoting and platform handling.

type Settings = {
  auto_discovery_key?: string;
  script_domain?: string;
  [key: string]: unknown;
};

type FixtureWindow = Window & {
  __AD_SETTINGS__?: Settings;
  __AD_VERSION__?: string;
  __AD_LOADING__?: boolean;
  autoDiscoveryFixture: {
    /** Replace the settings the section reads. The section re-renders. */
    setSettings: (next: Settings) => void;
    /** The generated command as rendered in the read-only textarea. */
    command: () => string;
    /** What the copy button put on the clipboard, or null. */
    clipboard: () => string | null;
    /** How many times fetch was called, and with which methods. */
    calls: () => string[];
  };
};

const i18n = i18next.createInstance();
await i18n.init({
  lng: "en",
  resources: {
    en: {
      translation: {
        "admin.nodeTable.autoDiscovery.tryIt": "Try auto discovery",
        "admin.nodeTable.autoDiscovery.disabledDescription":
          "Enable auto discovery and a node enrols itself with one command.",
        "admin.nodeTable.autoDiscovery.goToSettings": "Go to general settings",
        "admin.nodeTable.autoDiscovery.title": "Auto discovery",
        "admin.nodeTable.autoDiscovery.enabledDescription":
          "Run this on the target host and it registers itself.",
        "admin.nodeTable.installOptions": "Install options",
        "admin.nodeTable.disableWebSsh": "Disable web SSH",
        "admin.nodeTable.disableAutoUpdate": "Disable auto update",
        "admin.nodeTable.generatedCommand": "Generated command",
        "admin.nodeTable.installVersion": "Pin a version",
        "admin.nodeTable.ghproxy": "GitHub proxy",
        "admin.nodeTable.install_dir": "Install directory",
        "common.copy": "Copy",
        "copy_success": "Copied",
      },
    },
  },
});

const fixtureWindow = window as FixtureWindow;
const calls: string[] = [];

// `RPC2Client.call` prefers the WebSocket whenever it is CONNECTED and only falls
// back to HTTP otherwise — so without this the version request would go over a
// socket and never reach the stub below, leaving `isSnapshotBackend` false no
// matter what the fixture asked for. Failing the socket at construction pins the
// client to the HTTP path, which is the one this fixture can answer.
class OfflineSocket {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSING = 2;
  static readonly CLOSED = 3;
  readyState = OfflineSocket.CLOSED;
  onopen: (() => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onmessage: (() => void) | null = null;
  constructor() {
    setTimeout(() => this.onerror?.(), 0);
  }
  send() {
    /* nothing to send to */
  }
  close() {
    /* already closed */
  }
  addEventListener() {
    /* the client uses the on* properties */
  }
  removeEventListener() {
    /* the client uses the on* properties */
  }
}
(fixtureWindow as unknown as { WebSocket: unknown }).WebSocket = OfflineSocket;

// `loading` and the backend version are read once, at mount, because both are
// props or first-render facts. The spec picks a URL per case rather than trying
// to mutate them afterwards.
const params = new URLSearchParams(location.search);
const mountedLoading = params.get("loading") === "1";

/** One mutable settings object, so a test can change it after the mount. */
let settings: Settings = fixtureWindow.__AD_SETTINGS__ ?? {};
const version = params.get("version") ?? "v0.1.27";

window.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = String(input);
  calls.push(`${init?.method ?? "GET"} ${url}`);

  if (url === "/api/rpc2") {
    // `useIsSnapshotBackend` asks for the version through the same client.
    const body = typeof init?.body === "string" ? init.body : "";
    const method = (JSON.parse(body || "{}") as { method?: string }).method ?? "";
    if (method === "common:getVersion") {
      return new Response(JSON.stringify({ jsonrpc: "2.0", id: 1, result: { version } }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }
    return new Response(
      JSON.stringify({ jsonrpc: "2.0", id: 1, result: null }),
      { status: 200, headers: { "Content-Type": "application/json" } },
    );
  }
  throw new Error(`Unexpected request: ${url}`);
};

let clipboardText: string | null = null;
Object.defineProperty(navigator, "clipboard", {
  configurable: true,
  value: {
    writeText: async (text: string) => {
      clipboardText = text;
    },
  },
});

Object.assign(fixtureWindow, {
  autoDiscoveryFixture: {
    setSettings: () => undefined, // replaced below, once the setter exists
    command: () =>
      (document.querySelector("textarea") as HTMLTextAreaElement | null)?.value ?? "",
    clipboard: () => clipboardText,
    calls: () => [...calls],
  },
});

/**
 * The wrapper owns the settings state so `setSettings` re-renders the section —
 * the same way the real dialog re-renders when the page's settings load.
 *
 * Written as an inline component inside `createRoot` rather than a named
 * declaration: this file also exports the fixture object, and
 * `react-refresh/only-export-components` reads a capitalized function at module
 * scope as a component export.
 */
createRoot(document.getElementById("root")!).render(
  (function () {
    // The API is assigned during render so it cannot go stale if React discards
    // a render.
    const Fixture = ({ api }: { api: typeof fixtureWindow.autoDiscoveryFixture }) => {
      const [current, setCurrent] = useState<Settings>(settings);
      api.setSettings = (next: Settings) => {
        settings = next;
        setCurrent(next);
      };
      return (
        <I18nextProvider i18n={i18n}>
          <Theme>
            {/* BrowserRouter, not MemoryRouter: the real app mounts the panel
                under BrowserRouter (`main.tsx`). Under MemoryRouter the
                section's own `<Link>` did not render as an anchor, and the
                disabled branch's link target is what this fixture asserts. */}
            <BrowserRouter>
              <RPC2Provider>
                <AutoDiscoverySection settings={current} loading={mountedLoading} />
              </RPC2Provider>
            </BrowserRouter>
            <Toaster />
          </Theme>
        </I18nextProvider>
      );
    };
    return <Fixture api={fixtureWindow.autoDiscoveryFixture} />;
  })(),
);
