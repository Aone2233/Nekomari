import { createRoot } from "react-dom/client";
import i18next from "i18next";
import { I18nextProvider } from "react-i18next";
import { Theme } from "@radix-ui/themes";
import Sessions from "../src/pages/admin/sessions";

const i18n = i18next.createInstance();
await i18n.init({
  lng: "en",
  resources: {
    en: {
      translation: {
        "nodeCard.time_minute": "m",
        "nodeCard.time_second": "s",
        "time.ago": "ago",
        "sessions.session_id": "Session ID",
        "sessions.active_sessions": "Session details",
        "sessions.latest_online": "Latest online",
      },
    },
  },
});

const root = createRoot(document.getElementById("root")!);
root.render(
  <I18nextProvider i18n={i18n}>
    <Theme>
      <Sessions />
    </Theme>
  </I18nextProvider>,
);
Object.assign(window, { unmountAdminClockFixture: () => root.unmount() });
