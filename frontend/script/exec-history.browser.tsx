import { createRoot } from "react-dom/client";
import i18next from "i18next";
import { I18nextProvider } from "react-i18next";
import { Theme } from "@radix-ui/themes";
import { NodeDetailsProvider } from "../src/contexts/NodeDetailsProvider";
import ExecHistory from "../src/pages/admin/ExecHistory";
import en from "../src/i18n/locales/en.json";
import "@radix-ui/themes/styles.css";
import "../src/global.css";

const i18n = i18next.createInstance();
await i18n.init({ lng: "en", resources: { en: { translation: en } }, interpolation: { escapeValue: false } });
const appearance = new URLSearchParams(location.search).get("theme") === "dark" ? "dark" : "light";
createRoot(document.getElementById("root")!).render(
  <I18nextProvider i18n={i18n}><Theme appearance={appearance}><NodeDetailsProvider><main className="p-4"><ExecHistory /></main></NodeDetailsProvider></Theme></I18nextProvider>,
);
