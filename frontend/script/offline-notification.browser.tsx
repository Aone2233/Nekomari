import { createRoot } from "react-dom/client";
import i18next from "i18next";
import { I18nextProvider } from "react-i18next";
import { Theme } from "@radix-ui/themes";
import { Toaster } from "sonner";
import OfflinePage from "../src/pages/admin/notification/offline";
import "@radix-ui/themes/styles.css";
import "../src/global.css";

const i18n = i18next.createInstance();
await i18n.init({
  lng: "en",
  resources: {
    en: {
      translation: {
        common: {
          status: "Status", cancel: "Cancel", save: "Save", server: "Server",
          search: "Search", selected: "Selected: {{count}}", action: "Action",
          edit: "Edit", batch_edit: "Batch edit", enabled: "Enabled",
          disabled: "Disabled", updated_successfully: "Updated successfully",
        },
        notification: {
          offline: {
            full_title: "Offline notifications", grace_period: "Grace period",
            grace_period_tip: "Delay before sending", last_notified: "Last notified",
            never_triggered: "Never", tips: "",
          },
        },
        nodeCard: { time_second: "seconds" },
      },
    },
  },
});

createRoot(document.getElementById("root")!).render(
  <I18nextProvider i18n={i18n}>
    <Theme>
      <OfflinePage />
      <Toaster />
    </Theme>
  </I18nextProvider>,
);
