import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { Theme } from "@radix-ui/themes";
import "@radix-ui/themes/styles.css";
import "../global.css";
// 首帧要等当前语言：initI18n 会在这里按需加载当前语言的 chunk。
import { initI18n } from "../i18n/config";
import ErrorBoundary from "../components/ErrorBoundary";
import { RPC2Provider } from "../contexts/RPC2Provider";
import { ForecastShell } from "../pages/forecast";

/**
 * The standalone traffic forecast page, served at /standalone/forecast/forecast.html.
 *
 * The admin menu mounts the same page at /admin/forecast in the panel shell.
 * Keep this independent entry for old links: themes can replace the public
 * router, but not the panel-owned /admin shell.
 */
const root = document.getElementById("root");
if (!root) {
  throw new Error("the forecast page needs a #root element");
}

void initI18n().then(() => {
  createRoot(root).render(
    <StrictMode>
      <ErrorBoundary>
        <Theme appearance="inherit" scaling="110%" style={{ minHeight: "100vh" }}>
          <RPC2Provider>
            <ForecastShell />
          </RPC2Provider>
        </Theme>
      </ErrorBoundary>
    </StrictMode>,
  );
});
