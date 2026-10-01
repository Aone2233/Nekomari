/**
 * The sensitive OAuth2 binding endpoints, and how a 2FA code travels with them.
 *
 * Both are gated server-side by `api.RequireSensitive2FA()` (`web/router/router.go`), the same
 * gate the 2FA buttons on the account page already answer: the code is read from the query string
 * (`2fa_code` / `two_factor_code` / `otp`), a request header, or a JSON body
 * (`web/api/AuthSensitive.go`). Bind is a top-level redirect to the provider, so its code can only
 * ride the query string; unbind is a same-origin POST and carries it the way this page's existing
 * `/2fa/disable` call does — in the query string.
 *
 * An account **without** a factor is let through by the server itself (`VerifySensitive2FACore`:
 * `if user.TwoFactor == "" { return nil }`), so a code is only ever attached when the account has
 * one. Building that decision into the URL functions is what keeps the no-2FA behaviour byte-for
 * byte what it was before the gate existed: bare path, no query, nothing to prompt for.
 *
 * It lives in its own module so the URL shape is testable without mounting the page
 * (`frontend/script/oauth2-binding.test.mjs`).
 */

export const OAUTH2_BIND_PATH = "/api/admin/oauth2/bind";
export const OAUTH2_UNBIND_PATH = "/api/admin/oauth2/unbind";

/** The first query key `api.get2FACode` reads. */
export const SENSITIVE_2FA_QUERY_PARAM = "2fa_code";

/** codeQuery is "?2fa_code=..." for a real code, and "" when there is none to send. */
function codeQuery(code: string | null | undefined): string {
  const trimmed = (code ?? "").trim();
  if (!trimmed) {
    return "";
  }
  return `?${SENSITIVE_2FA_QUERY_PARAM}=${encodeURIComponent(trimmed)}`;
}

/** oauth2BindUrl is the top-level navigation target. Pass the code only when one was collected. */
export function oauth2BindUrl(code?: string | null): string {
  return OAUTH2_BIND_PATH + codeQuery(code);
}

/** oauth2UnbindUrl is the POST target. Pass the code only when one was collected. */
export function oauth2UnbindUrl(code?: string | null): string {
  return OAUTH2_UNBIND_PATH + codeQuery(code);
}
