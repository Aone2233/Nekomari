import { createRoot } from "react-dom/client";
import i18next from "i18next";
import { I18nextProvider } from "react-i18next";
import { Theme } from "@radix-ui/themes";
import { PingTaskProvider } from "../src/contexts/PingTaskProvider";
import LoadPage from "../src/pages/admin/notification/load";
import en from "../src/i18n/locales/en.json";
import "@radix-ui/themes/styles.css";
import "../src/global.css";

const i18n = i18next.createInstance();
await i18n.init({ lng: "en", resources: { en: { translation: en } }, interpolation: { escapeValue: false } });
createRoot(document.getElementById("root")!).render(
  <I18nextProvider i18n={i18n}><Theme><PingTaskProvider><LoadPage /></PingTaskProvider></Theme></I18nextProvider>,
);
