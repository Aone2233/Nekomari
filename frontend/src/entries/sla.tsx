import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { Theme } from "@radix-ui/themes";
import "@radix-ui/themes/styles.css";
import "../global.css";
// i18n must be initialized before any component renders.
import "../i18n/config";
import ErrorBoundary from "../components/ErrorBoundary";
import { RPC2Provider } from "../contexts/RPC2Provider";
import { NodeListProvider } from "../contexts/NodeListProvider";
import { SlaReportPage } from "../pages/sla";

/**
 * The standalone SLA report, served at `/sla.html` (roadmap H1, decision in H0).
 *
 * **Why this is not a route in `routes.ts`.** The panel's UI can be replaced wholesale by
 * an installed theme: LuminaPlus ships its own compiled `index.html` and `assets`, and
 * `web/public/public.go` prefers the theme directory over the embedded default frontend.
 * A route added to the built-in router therefore does not exist in a themed deployment —
 * v0.1.31 shipped exactly that way, and the production panel answered its own 404.
 *
 * A page at a path the theme does not claim falls through to the embedded bundle, so it
 * works in every deployment, themed or not. The cost, stated plainly: it does not inherit
 * the theme's chrome or navigation. That is the trade the alternative — injecting a
 * script that renders the whole feature into the theme's DOM — does not avoid either,
 * because the theme exposes no route registration to hook.
 *
 * It bundles its own React rather than sharing the main app's chunks, which is what makes
 * it self-contained. The duplication is the price of working under a theme; a `chunk-*`
 * asset split would resolve against the theme's bundle and fail.
 */

const root = document.getElementById("root");
if (!root) {
  throw new Error("the SLA page needs a #root element");
}

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
