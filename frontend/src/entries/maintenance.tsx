import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { Theme } from "@radix-ui/themes";
import "@radix-ui/themes/styles.css";
import "../global.css";
// i18n must be initialized before any component renders.
import "../i18n/config";
import ErrorBoundary from "../components/ErrorBoundary";
import { RPC2Provider } from "../contexts/RPC2Provider";
import { MaintenanceShell } from "../pages/maintenance";

/**
 * The standalone maintenance window page, served at /standalone/maintenance/maintenance.html.
 *
 * Why this is not a route in `routes.ts`: the panel's UI can be replaced wholesale by an installed
 * theme, and web/public/public.go prefers the theme directory over the embedded bundle — so a route
 * added to the built-in router does not exist in a themed deployment. Roadmap H0 records the
 * finding; v0.1.31 shipped that way and answered its own 404.
 */
const root = document.getElementById("root");
if (!root) {
  throw new Error("the maintenance page needs a #root element");
}

createRoot(root).render(
  <StrictMode>
    <ErrorBoundary>
      <Theme appearance="inherit" scaling="110%" style={{ minHeight: "100vh" }}>
        <RPC2Provider>
          <MaintenanceShell />
        </RPC2Provider>
      </Theme>
    </ErrorBoundary>
  </StrictMode>,
);