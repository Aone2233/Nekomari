import { createRoot } from "react-dom/client";
import i18next from "i18next";
import { I18nextProvider } from "react-i18next";
import { Theme } from "@radix-ui/themes";
import { Toaster } from "sonner";
import { NodeDetailsProvider } from "../src/contexts/NodeDetailsProvider";
import { RPC2Provider } from "../src/contexts/RPC2Provider";
import { Layout } from "../src/pages/admin/index";
import "@radix-ui/themes/styles.css";

// Mounts the real admin page body — not a copy of its logic — so the state
// wiring the file owns is under test: the 5s poll and its teardown, the search
// filter, the weight sort, and the selection shared between Header and
// NodeTable. NodeTable receives `nodes` as a prop and owns only the reorder
// state, so mounting it alone would leave the filter, the sort and the poll
// untested.
//
// The page reads the node list and the settings from `fetch`, so both are
// scripted from the test. Everything else is the real component tree.

const i18n = i18next.createInstance();
await i18n.init({
  lng: "en",
  resources: {
    en: {
      translation: {
        "admin.nodeTable.nodeList": "Node list",
        "admin.nodeTable.searchByName": "Search by name",
        "admin.nodeTable.addNode": "Add node",
        "admin.nodeTable.nameOptional": "Name (optional)",
        "admin.nodeTable.name": "Name",
        "admin.nodeTable.billing": "Billing",
        "admin.nodeTable.dragToReorder": "Drag to reorder",
        "admin.nodeTable.autoDiscovery": "Auto discovery",
        "admin.nodeDetail.ipAddress": "IP address",
        "admin.nodeDetail.clientVersion": "Version",
        "admin.nodeDetail.machineDetail": "Machine details",
        "admin.nodeEdit.editInfo": "Edit",
        "admin.nodeEdit.remark": "Remark",
        "common.group": "Group",
        "common.delete": "Delete",
        "common.cancel": "Cancel",
        "common.save": "Save",
        "common.confirm_delete": "Confirm",
        "copy_success": "Copied",
        "terminal.title": "Terminal",
      },
    },
  },
});

type QueuedResponse = {
  /** Hold this response until the key is released from the test. */
  hold?: string;
  status?: number;
  body?: unknown;
  /** Reject the fetch instead of resolving it. */
  reject?: string;
};

const NODE_LIST = "/api/admin/client/list";
const SETTINGS = "/api/admin/settings";

// The page fetches on mount, so the queue has to exist before the app script
// runs. The test seeds it with `page.add_init_script` before navigating; the
// array is then shared, and a test can push to it from `page.evaluate` for a
// later poll.
type FixtureWindow = Window & {
  nodeListQueue: QueuedResponse[];
  nodeListHolds: Map<string, (response: QueuedResponse) => void>;
  adminNodeTableFixture: {
    queue: (response: QueuedResponse) => void;
    isHeld: (key: string) => boolean;
    release: (key: string, response: QueuedResponse) => Promise<void>;
    urlCalls: () => string[];
    pending: () => number;
    renderedRows: () => string[];
  };
};

const fixtureWindow = window as FixtureWindow;
// Seeded by `page.add_init_script` before the app script runs; the fallbacks
// matter because Vite re-executes this module on a hot update without re-running
// the init script.
const queued: QueuedResponse[] = fixtureWindow.nodeListQueue ?? [];
const holds: Map<string, (response: QueuedResponse) => void> =
  fixtureWindow.nodeListHolds ?? new Map();
fixtureWindow.nodeListQueue = queued;
fixtureWindow.nodeListHolds = holds;
const urlCalls: string[] = [];

/** The last settings response is reused, so the settings store never blocks. */
const settingsPayload = {
  sitename: "Fixture",
  description: "",
  script_domain: "",
  metric_available: true,
  cors_origin_check_enabled: true,
  geo_ip_enabled: false,
  geo_ip_provider: "",
  o_auth_provider: "",
  o_auth_enabled: false,
  ssrf_protection_enabled: false,
  custom_head: "",
};

window.fetch = async (input: RequestInfo | URL) => {
  const url = String(input);
  urlCalls.push(url);

  if (url === SETTINGS) {
    return new Response(JSON.stringify({ status: "success", data: settingsPayload }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }
  if (url !== NODE_LIST) {
    throw new Error(`Unexpected request: ${url}`);
  }

  let next = queued.shift();
  if (!next) throw new Error("No queued node-list response");
  if (next.hold) {
    const key = next.hold;
    next = await new Promise<QueuedResponse>((resolve) => holds.set(key, resolve));
  }
  if (next.reject) throw new Error(next.reject);
  return new Response(JSON.stringify(next.body), {
    status: next.status ?? 200,
    headers: { "Content-Type": "application/json" },
  });
};

Object.assign(fixtureWindow, {
  adminNodeTableFixture: {
    queue: (response: QueuedResponse) => queued.push(response),
    isHeld: (key: string) => holds.has(key),
    release: async (key: string, response: QueuedResponse) => {
      const complete = holds.get(key);
      if (!complete) throw new Error(`No held response: ${key}`);
      holds.delete(key);
      complete(response);
      await new Promise((resolve) =>
        requestAnimationFrame(() => requestAnimationFrame(resolve)),
      );
    },
    urlCalls: () => [...urlCalls],
    pending: () => queued.length,
    // The rows as the table actually renders them, for `wait_for_function`: a
    // test can then wait on the rendered order without polling from Python.
    renderedRows: () =>
      Array.from(document.querySelectorAll('[data-testid="node-row"]')).map(
        (row) => row.getAttribute("data-node-uuid") ?? "",
      ),
  },
});

createRoot(document.getElementById("root")!).render(
  <I18nextProvider i18n={i18n}>
    <Theme>
      {/* The page reads settings through a context and one of its rows asks the
          RPC2 client whether the backend is the snapshot build, so both
          providers have to be the real ones. */}
      <RPC2Provider>
        <NodeDetailsProvider>
          <Layout />
        </NodeDetailsProvider>
      </RPC2Provider>
      <Toaster />
    </Theme>
  </I18nextProvider>,
);
