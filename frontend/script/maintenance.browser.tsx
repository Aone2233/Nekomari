import { createRoot } from "react-dom/client";
import i18next from "i18next";
import { I18nextProvider } from "react-i18next";
import { Theme } from "@radix-ui/themes";
import { MaintenancePage, type MaintenanceWindow } from "../src/components/maintenance/MaintenancePage";
import "@radix-ui/themes/styles.css";

/**
 * Mounted fixture for the maintenance window page (roadmap H3).
 *
 * The page takes its list and its write callbacks as props, so every state worth asserting is
 * reachable without a server:
 *
 *   ?state=ready    an open window, a scoped one, and an expired one
 *   ?state=empty    no windows
 *   ?state=refused  saving is refused, with the server's reason
 *   ?state=loading  nothing loaded yet
 *
 * The assertion the feature lives on is not about rendering at all: **an open window must be
 * distinguishable from a closed one**, and a scoped window from a fleet-wide one. An operator
 * cannot check the notifier's behaviour from this page, so the page has to be unambiguous about
 * what is currently suppressed.
 *
 * `open` and `remaining_seconds` are supplied the way the server supplies them rather than computed
 * here, because recomputing them would be a second definition of "open" — the fixture would then be
 * testing the page's arithmetic rather than its presentation.
 */

const i18n = i18next.createInstance();
await i18n.init({
  lng: "en",
  resources: {
    en: {
      translation: {
        "maintenance.title": "Maintenance windows",
        "maintenance.subtitle": "While a window is open, its nodes' alerts are suppressed.",
        "maintenance.creating": "New window",
        "maintenance.editing": "Editing",
        "maintenance.new": "new",
        "maintenance.namePlaceholder": "name",
        "maintenance.reasonPlaceholder": "reason (optional)",
        "maintenance.scope": "Scope",
        "maintenance.scopeAll": "every node",
        "maintenance.scopeAllBadge": "every node",
        "maintenance.save": "Save window",
        "maintenance.cancelEdit": "Cancel edit",
        "maintenance.windowsHeading": "Windows",
        "maintenance.openCount": "{{count}} open now",
        "maintenance.loading": "Loading windows…",
        "maintenance.empty": "No maintenance windows.",
        "maintenance.name": "Name",
        "maintenance.window": "Window",
        "maintenance.state": "State",
        "maintenance.openFor": "open, {{remaining}} left",
        "maintenance.closed": "closed",
        "maintenance.edit": "Edit",
        "maintenance.delete": "Delete",
        "maintenance.error": "The window was not saved",
      },
    },
  },
});

const NOW = Date.now();
const iso = (offsetMinutes: number) => new Date(NOW + offsetMinutes * 60_000).toISOString();

const WINDOWS: MaintenanceWindow[] = [
  {
    id: 1,
    name: "kernel upgrade",
    start: iso(-30),
    end: iso(90),
    clients: [],
    reason: "reboot for the new kernel",
    open: true,
    covers_everything: true,
    remaining_seconds: 90 * 60,
  },
  {
    id: 2,
    name: "switch swap",
    start: iso(-10),
    end: iso(20),
    clients: ["uuid-a"],
    open: true,
    covers_everything: false,
    remaining_seconds: 20 * 60,
  },
  {
    id: 3,
    name: "last week's migration",
    start: iso(-7 * 24 * 60),
    end: iso(-6 * 24 * 60),
    clients: [],
    open: false,
    covers_everything: true,
    remaining_seconds: 0,
  },
];

const params = new URLSearchParams(globalThis.location.search);
const state = params.get("state") ?? "ready";

declare global {
  interface Window {
    __maintenanceSaves?: Array<Record<string, unknown>>;
    __maintenanceDeletes?: number[];
  }
}
globalThis.__maintenanceSaves = [];
globalThis.__maintenanceDeletes = [];

const save = async (window: Record<string, unknown>) => {
  globalThis.__maintenanceSaves!.push({ ...window });
  if (state === "refused") {
    // The server's own reason, which the page must show rather than a generic message.
    throw new Error("end must be after start");
  }
};

const remove = async (id: number) => {
  globalThis.__maintenanceDeletes!.push(id);
};

const root = createRoot(document.getElementById("root")!);
root.render(
  <I18nextProvider i18n={i18n}>
    <Theme appearance="dark">
      <MaintenancePage
        windows={state === "empty" || state === "loading" ? [] : WINDOWS}
        save={save}
        remove={remove}
        nodeUUIDs={["uuid-a", "uuid-b"]}
        loading={state === "loading"}
      />
    </Theme>
  </I18nextProvider>,
);
