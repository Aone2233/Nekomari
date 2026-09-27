import { createRoot } from "react-dom/client";
import i18next from "i18next";
import { I18nextProvider } from "react-i18next";
import { Theme } from "@radix-ui/themes";
import { ConfigExportPage } from "../src/components/config/ConfigExportPage";
import type { ImportPlan } from "../src/components/config/ConfigExportPage";
import "@radix-ui/themes/styles.css";

/**
 * Mounted fixture for the configuration export/import page (roadmap H4).
 *
 * The page takes its three operations as props, so the states worth asserting are reachable without
 * a server:
 *
 *   ?state=ready     export works, the dry run reports creates and updates
 *   ?state=removals  the plan includes records the panel has and the document does not
 *   ?state=refused   the import is refused, with the server's reason
 *
 * The assertion the feature lives on is not about rendering: **Import is locked until a dry run has
 * been shown for the document currently in the box.** An import rewrites nodes, tasks, windows and
 * settings at once, and a page that offered only "Import" would be asking for trust in a file.
 *
 * English resources only; the assertions read DOM attributes.
 */

const i18n = i18next.createInstance();
await i18n.init({
  lng: "en",
  resources: {
    en: {
      translation: {
        "config.title": "Configuration export and import",
        "config.subtitle": "Take the panel's configuration as one file.",
        "config.export": "Export",
        "config.includeSecrets": "Include credentials",
        "config.secretsWarning": "The file will carry credentials",
        "config.documentHeading": "Document",
        "config.documentPlaceholder": "paste",
        "config.dryRun": "Check what this would do",
        "config.import": "Import",
        "config.parseError": "Not a document",
        "config.runDryRunFirst": "Check the document first.",
        "config.error": "The operation failed",
        "config.planSummary": "This would:",
        "config.resultSummary": "Imported:",
        "config.changeKind": "Action",
        "config.changeEntity": "Entity",
        "config.changeRecord": "Record",
        "config.changeFields": "Fields",
        "config.kind_create": "create",
        "config.kind_update": "update",
        "config.kind_unchanged": "unchanged",
        "config.kind_remove": "not in the document",
        "config.removalsNotDeleted": "{{count}} record(s) are in the panel and not in the document; they were left alone.",
      },
    },
  },
});

const DOCUMENT = {
  schema_version: 1,
  exported_at: "2026-09-27T12:00:00Z",
  secrets_included: false,
  clients: [{ uuid: "uuid-a", name: "Tokyo", group: "asia" }],
  ping_tasks: [],
  maintenance_windows: [],
  settings: { sitename: "Nekomari Monitor" },
  notification_configs: [],
};

const params = new URLSearchParams(globalThis.location.search);
const state = params.get("state") ?? "ready";

declare global {
  interface Window {
    __configCalls?: { export?: number; plan?: number; import?: number };
  }
}
globalThis.__configCalls = {};

const planFor = (document: Record<string, unknown>): ImportPlan => {
  const base: ImportPlan = {
    schema_version: 1,
    creates: 2,
    updates: 1,
    unchanged: 3,
    removals: state === "removals" ? 2 : 0,
    changes: [
      { kind: "create", entity: "clients", id: "uuid-new", name: "Added node" },
      { kind: "create", entity: "settings", id: "sitename" },
      { kind: "update", entity: "clients", id: "uuid-a", name: "Tokyo", fields: ["group"] },
      { kind: "unchanged", entity: "ping_tasks", id: "1", name: "cloudflare" },
    ],
    warnings: [],
  };
  if (state === "removals") {
    base.changes!.push(
      { kind: "remove", entity: "clients", id: "uuid-extra", name: "Not in the document" },
      { kind: "remove", entity: "clients", id: "uuid-extra-2", name: "Also not in it" },
    );
    base.warnings!.push("2 record(s) are in the panel and not in the document; an import does not delete them");
  }
  void document;
  return base;
};

const exportConfig = async (includeSecrets: boolean) => {
  globalThis.__configCalls!.export = (globalThis.__configCalls!.export ?? 0) + 1;
  const document: Record<string, unknown> = { ...DOCUMENT, secrets_included: includeSecrets };
  if (includeSecrets) {
    (document.clients as Array<Record<string, unknown>>)[0].token = "agent-token-a";
  }
  return document;
};

const planImport = async (document: Record<string, unknown>) => {
  globalThis.__configCalls!.plan = (globalThis.__configCalls!.plan ?? 0) + 1;
  return planFor(document);
};

const importConfig = async (document: Record<string, unknown>) => {
  globalThis.__configCalls!.import = (globalThis.__configCalls!.import ?? 0) + 1;
  if (state === "refused") {
    throw new Error("the document is schema_version 2 and this panel understands 1");
  }
  return planFor(document);
};

const root = createRoot(document.getElementById("root")!);
root.render(
  <I18nextProvider i18n={i18n}>
    <Theme appearance="dark">
      <ConfigExportPage
        exportConfig={exportConfig}
        planImport={planImport}
        importConfig={importConfig}
      />
    </Theme>
  </I18nextProvider>,
);
