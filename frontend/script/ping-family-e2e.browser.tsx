import { createRoot } from "react-dom/client";
import i18next from "i18next";
import { I18nextProvider } from "react-i18next";
import { Theme } from "@radix-ui/themes";
import { Toaster } from "sonner";
import { NodeDetailsContext, type NodeDetail } from "../src/contexts/NodeDetailsContext";
import { PingTaskContext } from "../src/contexts/PingTaskContext";
import { AddButton } from "../src/pages/admin/pingTask";
import translations from "../src/i18n/locales/en.json";
import "@radix-ui/themes/styles.css";

const i18n = i18next.createInstance();
await i18n.init({ lng: "en", resources: { en: { translation: translations } } });
const nodes = ["browser-ipv6", "browser-nofamily", "browser-disabled"].map((uuid, weight) => ({ uuid, name: uuid, weight })) as NodeDetail[];
const fixture = { refreshes: 0 };
Object.assign(window, { isolatedPingAcceptance: fixture });
createRoot(document.getElementById("root")!).render(
  <I18nextProvider i18n={i18n}>
    <Theme>
      <NodeDetailsContext.Provider value={{ nodeDetail: nodes, isLoading: false, error: null, refresh: () => {} }}>
        <PingTaskContext.Provider value={{ pingTasks: [], isLoading: false, error: null, refresh: () => { fixture.refreshes++; } }}>
          <AddButton pinnedFamilyAvailable />
          <Toaster />
        </PingTaskContext.Provider>
      </NodeDetailsContext.Provider>
    </Theme>
  </I18nextProvider>,
);
