/**
 * The panel's own interface, built as a self-contained bundle and served at `/admin/`.
 *
 * ## Why this exists
 *
 * Roadmap H7. The panel's admin pages have always come from the **embedded default theme**:
 * `web/public/public.go` forces `/admin` and `/terminal` to that theme rather than to whatever theme is
 * installed, so originally `komari-web` supplied both a front page and an admin interface. Making
 * LuminaPlus the embedded default removed the admin half — its router declares only `/`,
 * `/instance/:uuid`, `/assets`, `/traffic` and `/404`, and everything under `/admin` is its
 * service-worker recovery screen — so a fresh installation had no way to administer the panel.
 *
 * The fix is to stop treating the admin interface as a theme's responsibility. A theme may decide what
 * the *public* page looks like; whether the panel can be administered at all should not depend on which
 * theme is installed.
 *
 * ## Why this is the whole app rather than an admin-only router
 *
 * `App` owns the router and already serves every route, so mounting it here is the same interface a
 * fresh installation has always had, at the same paths. That matters beyond saving work:
 *
 *   - **`/admin/` is the base, so nothing needs rewriting.** The panel's own calls are absolute
 *     (`/api/admin/...`), its routes are absolute (`/admin/servers`), and the server will serve this
 *     bundle for `/admin` and everything under it. A different prefix would need a router basename and
 *     would move the admin away from the path every existing link and bookmark uses.
 *   - The public routes come along, which is harmless: they are the same pages the theme already
 *     provides, reachable at `/` for anyone who wants the built-in look.
 *
 * `BrowserRouter` rather than a hash router, because the server can answer any path under `/admin` with
 * this one document — see the `/admin` handling in `web/public/public.go`.
 */
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import "@radix-ui/themes/styles.css";
import "../global.css";
// i18n must be initialized before any component renders.
import "../i18n/config";
import ErrorBoundary from "../components/ErrorBoundary";
import { App } from "../App";

const root = document.getElementById("root");
if (!root) {
  throw new Error("the admin bundle needs a #root element");
}

createRoot(root).render(
  <ErrorBoundary>
    <StrictMode>
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </StrictMode>
  </ErrorBoundary>,
);