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
import { BulkPage } from "../pages/bulk";

/**
 * The standalone bulk edit page, served at `/standalone/bulk/bulk.html`.
 *
 * The admin menu mounts the same page at /admin/bulk in the panel shell.
 * This independent entry preserves old links even when a theme replaces the
 * public router; /admin itself is panel-owned and cannot be shadowed.
 *
 * It bundles its own React rather than sharing the main app's chunks, for the reason in
 * vite.standalone.shared.ts: a shared chunk would land in the `/assets/` namespace the theme
 * owns, where a name collision resolves to the theme's file.
 */
const root = document.getElementById("root");
if (!root) {
  throw new Error("the bulk page needs a #root element");
}

void initI18n().then(() => {
  createRoot(root).render(
    <StrictMode>
      <ErrorBoundary>
        <Theme appearance="inherit" scaling="110%" style={{ minHeight: "100vh" }}>
          <RPC2Provider>
            <NodeListProvider>
              <BulkPage />
            </NodeListProvider>
          </RPC2Provider>
        </Theme>
      </ErrorBoundary>
    </StrictMode>,
  );
});
