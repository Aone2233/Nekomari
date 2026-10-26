import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { Theme } from "@radix-ui/themes";
import "@radix-ui/themes/styles.css";
import "../global.css";
// 首帧要等当前语言：initI18n 会在这里按需加载当前语言的 chunk。
import { initI18n } from "../i18n/config";
import ErrorBoundary from "../components/ErrorBoundary";
import { RPC2Provider } from "../contexts/RPC2Provider";
import { NodeListProvider } from "../contexts/NodeListProvider";
import { SlaReportPage } from "../pages/sla";

/**
 * The standalone SLA report, served at `/sla.html` (roadmap H1, decision in H0).
 *
 * The admin menu now mounts this report at /admin/sla in the shared panel shell.
 * Keep this separate entry for public sharing and old bookmarks: an installed
 * theme replaces the public router, but cannot shadow the panel-owned /admin.
 *
 * It bundles its own React rather than sharing the main app's chunks, which is what makes
 * it self-contained. The duplication is the price of working under a theme; a `chunk-*`
 * asset split would resolve against the theme's bundle and fail.
 */

const root = document.getElementById("root");
if (!root) {
  throw new Error("the SLA page needs a #root element");
}

void initI18n().then(() => {
  createRoot(root).render(
    <StrictMode>
      <ErrorBoundary>
        <Theme appearance="inherit" scaling="110%" style={{ minHeight: "100vh" }}>
          <RPC2Provider>
            <NodeListProvider>
              <SlaReportPage />
            </NodeListProvider>
          </RPC2Provider>
        </Theme>
      </ErrorBoundary>
    </StrictMode>,
  );
});
