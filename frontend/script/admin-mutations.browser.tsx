import { createRoot } from "react-dom/client";
import i18next from "i18next";
import { I18nextProvider } from "react-i18next";
import { Theme } from "@radix-ui/themes";
import { Toaster } from "sonner";
import { NodeDetailsContext, type NodeDetail } from "../src/contexts/NodeDetailsContext";
import {
  BillingButton,
  DeleteButton,
  EditButton,
} from "../src/pages/admin/nodeTable/NodeDialogs";
import "@radix-ui/themes/styles.css";

const i18n = i18next.createInstance();
await i18n.init({
  lng: "en",
  resources: { en: { translation: {
    "common.delete": "Delete",
    "common.cancel": "Cancel",
    "common.confirm_delete": "Confirm delete",
    "common.save": "Save",
    "admin.nodeEdit.editInfo": "Edit info",
    "admin.nodeTable.billing": "Billing",
  } } },
});

const responses: Array<{ status: number; body: unknown }> = [];
const calls: string[] = [];
let refreshes = 0;
window.fetch = async (input) => {
  calls.push(String(input));
  const response = responses.shift();
  if (!response) throw new Error("No queued mutation response");
  return new Response(JSON.stringify(response.body), {
    status: response.status,
    headers: { "Content-Type": "application/json" },
  });
};

Object.assign(window, {
  mutationFixture: {
    respond: (status: number, body: unknown) => responses.push({ status, body }),
    calls: () => [...calls],
    refreshes: () => refreshes,
  },
});

const node = {
  uuid: "node-1", name: "alpha", token: "fixture-token", price: 5,
  billing_cycle: 30, hidden: false, traffic_limit: 0, traffic_limit_type: "sum",
} as NodeDetail;
createRoot(document.getElementById("root")!).render(
  <I18nextProvider i18n={i18n}>
    <Theme>
      <NodeDetailsContext.Provider value={{
        nodeDetail: [node], isLoading: false, error: null,
        refresh: () => { refreshes++; },
      }}>
        <DeleteButton node={node} />
        <EditButton node={node} />
        <BillingButton node={node} />
        <Toaster />
      </NodeDetailsContext.Provider>
    </Theme>
  </I18nextProvider>,
);
