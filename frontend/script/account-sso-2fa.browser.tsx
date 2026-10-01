import { createRoot } from "react-dom/client";
import i18next from "i18next";
import { I18nextProvider } from "react-i18next";
import { Theme } from "@radix-ui/themes";
import { Toaster } from "sonner";
import Account from "../src/pages/admin/account";
import "@radix-ui/themes/styles.css";

/**
 * Mounted fixture for the account page's SSO buttons (roadmap: sensitive-2FA gate).
 *
 * `/api/admin/oauth2/bind` and `/unbind` both sit behind `api.RequireSensitive2FA()`, so the page
 * has to collect the same one-time code its 2FA buttons already collect — and add *nothing* when
 * the account has no factor, which the server lets through. Both directions are driven with the
 * real page:
 *
 *   ?bound=0|1   no external account bound / one bound
 *   ?tfa=0|1     no factor enabled / factor enabled
 *   ?unbind=NNN  status the unbind POST answers with (default 200)
 *
 * `/api/me` and the unbind POST are answered here. The bind request is deliberately **not**
 * stubbed: it must reach the network so Chromium performs the real `redirect: "manual"` handling
 * the page's failure path depends on. The spec intercepts it with `page.route` and asserts on the
 * URL it was asked for.
 */
const i18n = i18next.createInstance();
await i18n.init({
  lng: "en",
  resources: {
    en: {
      translation: {
        "account.title": "Account",
        "account.greeting": "Hello, {{username}}",
        "account.change_username_title": "Change username",
        "account.change_username_button": "Change username",
        "account.change_password_title": "Change password",
        "account.new_password": "New password",
        "account.new_password_repeat": "Repeat new password",
        "account.change_password_button": "Change password",
        "account.2fa_disabled": "2FA is not enabled",
        "account.2fa_enabled": "2FA is enabled",
        "account.enable_2fa": "Enable 2FA",
        "account.disable_2fa": "Disable 2FA",
        "account.2fa_rebind": "Replace authenticator",
        "account.2fa_otp_input_prompt": "Enter the code from your authenticator",
        "account.otp_empty_error": "Empty otp code",
        "account_settings.sso_account": "External Account",
        "account_settings.sso_account_bound": "{{name}} account",
        "account_settings.sso_bound": "Bound External Account",
        "account_settings.sso_not_bound": "Unbound",
        "account_settings.bind_sso": "Bind External Account",
        "account_settings.unbind_sso": "Unbind External Account",
        "account_settings.confirm_unbind": "Confirm Unbind",
        "account_settings.unbind_sso_warning": "Unbind the {{provider}} account?",
        "account_settings.unbind_sso_failed": "Unbinding failed: {{error}}",
        "account_settings.unbind_sso_success": "Successfully unbound",
        "account_settings.sso_auth_failed": "SSO authentication failed",
        "account_settings.looking_for_backup": "Looking for a backup?",
        "common.cancel": "Cancel",
        "common.confirm": "Confirm",
        "common.updated_successfully": "Updated successfully",
        "common.unknownError": "Unknown error",
      },
    },
  },
});

const params = new URLSearchParams(globalThis.location.search);
const bound = params.get("bound") === "1";
const twoFactorEnabled = params.get("tfa") === "1";
const unbindStatus = Number(params.get("unbind") ?? "200");

const calls: string[] = [];
const realFetch = globalThis.fetch.bind(globalThis);

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
  const url =
    typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
  if (url.startsWith("/api/me")) {
    calls.push("GET /api/me");
    return json({
      logged_in: true,
      sso_id: bound ? "github_98765" : "",
      sso_type: "github",
      username: "admin",
      uuid: "u-1",
      "2fa_enabled": twoFactorEnabled,
    });
  }
  if (url.startsWith("/api/admin/oauth2/unbind")) {
    calls.push(`${init?.method ?? "GET"} ${url}`);
    return unbindStatus === 200
      ? json({ status: "success", message: "", data: null })
      : json({ status: "error", message: "Invalid 2FA code" }, unbindStatus);
  }
  // Everything else goes to the network — including the bind probe, whose behaviour depends on
  // the browser's own redirect handling rather than on anything a stub could reproduce.
  return realFetch(input as RequestInfo, init);
}) as typeof fetch;

Object.assign(globalThis, { ssoFixture: { calls: () => [...calls] } });

createRoot(document.getElementById("root")!).render(
  <I18nextProvider i18n={i18n}>
    <Theme>
      <Account />
      <Toaster />
    </Theme>
  </I18nextProvider>,
);
