import { createRoot } from "react-dom/client";
import i18next from "i18next";
import { I18nextProvider } from "react-i18next";
import { Theme } from "@radix-ui/themes";
import { BulkEditPage } from "../src/components/bulk/BulkEditPage";
import type { BulkReport } from "../src/types/Bulk";
import "@radix-ui/themes/styles.css";

/**
 * Mounted fixture for the bulk edit page (roadmap H2).
 *
 * The page takes its node list and its apply callback as props, so every state worth
 * asserting is reachable without a server:
 *
 *   ?state=ready      three nodes, apply succeeds for all
 *   ?state=partial    apply succeeds for two and fails for one, with reasons
 *   ?state=refresh    the node list is replaced (as a poll would) after mount
 *   ?state=loading    no nodes yet
 *
 * The most important assertion is not about failures at all: it is that **a field which is
 * switched off is not in the request**. Sending every field would set a whole fleet's group to
 * the empty string the first time anyone pressed apply, so the fixture records the payload it
 * was handed and the spec reads it.
 *
 * English resources only; the assertions read DOM attributes and recorded payloads.
 */

const i18n = i18next.createInstance();
await i18n.init({
  lng: "en",
  resources: {
    en: {
      translation: {
        "bulk.title": "Bulk edit",
        "bulk.subtitle": "Apply one change to many nodes.",
        "bulk.fieldsHeading": "Fields to change",
        "bulk.fieldsHint": "A field is only sent when its switch is on.",
        "bulk.nodesHeading": "Nodes",
        "bulk.selectAll": "Select all",
        "bulk.selectNone": "Select none",
        "bulk.selected": "Selected",
        "bulk.node": "Node",
        "bulk.group": "Group",
        "bulk.weight": "Weight",
        "bulk.apply": "Apply to selection",
        "bulk.selectionCount": "{{count}} selected",
        "bulk.fieldCount": "{{count}} fields to send",
        "bulk.noFieldsHint": "Switch on at least one field.",
        "bulk.loading": "Loading nodes…",
        "bulk.error": "The edit could not be sent",
        "bulk.reportSummary": "{{applied}} applied, {{failed}} failed of {{total}}",
        "bulk.failuresHeading": "Not applied",
        "bulk.unknownError": "no reason given",
        "bulk.fieldsApplied": "Fields sent: {{fields}}",
        "bulk.field.group": "Group",
        "bulk.field.tags": "Tags",
        "bulk.field.weight": "Weight",
        "bulk.field.hidden": "Hidden",
        "bulk.field.price": "Price",
        "bulk.field.billingCycle": "Billing cycle",
        "bulk.field.currency": "Currency",
        "bulk.field.trafficLimit": "Traffic limit",
        "bulk.field.trafficLimitType": "Limit type",
        "bulk.hidden.yes": "hidden",
        "bulk.hidden.no": "visible",
      },
    },
  },
});

const NODES = [
  { uuid: "uuid-a", name: "Tokyo Relay", group: "asia", weight: 1, hidden: false },
  { uuid: "uuid-b", name: "Frankfurt Box", group: "eu", weight: 2, hidden: false },
  { uuid: "uuid-c", name: "Silent Node", group: "asia", weight: 3, hidden: true },
];

const params = new URLSearchParams(globalThis.location.search);
const state = params.get("state") ?? "ready";

// The recorded calls, exposed for the spec to read. A fixture that only rendered would not be
// able to assert the one thing that matters most here: what was in the request.
declare global {
  interface Window {
    __bulkCalls?: Array<{ uuids: string[]; update: Record<string, unknown> }>;
    __setNodes?: (nodes: typeof NODES) => void;
  }
}
globalThis.__bulkCalls = [];

const report = (uuids: string[], failed: Record<string, string>): BulkReport => ({
  applied: uuids.filter((uuid) => !failed[uuid]).length,
  failed: uuids.filter((uuid) => failed[uuid]).length,
  total: uuids.length,
  field_names: [],
  outcomes: uuids.map((uuid) => ({ uuid, ok: !failed[uuid], error: failed[uuid] })),
});

const APPLY: Record<string, (uuids: string[], update: Record<string, unknown>) => Promise<BulkReport>> = {
  ready: async (uuids, update) => {
    globalThis.__bulkCalls!.push({ uuids: [...uuids], update: { ...update } });
    const result = report(uuids, {});
    result.field_names = Object.keys(update).sort();
    return result;
  },
  partial: async (uuids, update) => {
    globalThis.__bulkCalls!.push({ uuids: [...uuids], update: { ...update } });
    // One node refuses, with the applier's own reason, which is what the page must show.
    const result = report(uuids, { "uuid-b": "traffic_limit must be a valid non-negative int64 value" });
    result.field_names = Object.keys(update).sort();
    return result;
  },
  refresh: async (uuids, update) => {
    globalThis.__bulkCalls!.push({ uuids: [...uuids], update: { ...update } });
    return report(uuids, {});
  },
};

const root = createRoot(document.getElementById("root")!);

const render = (nodes: typeof NODES, apply = APPLY[state] ?? APPLY.ready) => {
  root.render(
    <I18nextProvider i18n={i18n}>
      <Theme appearance="dark">
        <BulkEditPage nodes={nodes} apply={apply} loading={state === "loading"} />
      </Theme>
    </I18nextProvider>,
  );
};

globalThis.__setNodes = (nodes: typeof NODES) => render(nodes);
render(state === "loading" ? [] : NODES);
