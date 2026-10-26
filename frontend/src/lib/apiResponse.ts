import { localizeServerError } from "./serverError";
import i18n from "i18next";

export type ApiSuccess<T = unknown> = {
  status: "success";
  message?: string;
  data?: T;
  [key: string]: unknown;
};

/** REST envelopes only: HTTP success cannot turn a business refusal into a save. */
export async function requireApiSuccess<T = unknown>(
  response: Response,
  invalidMessage = "Invalid API response",
): Promise<ApiSuccess<T>> {
  const payload: unknown = await response.json().catch(() => null);
  return assertApiSuccess<T>(response, payload, invalidMessage);
}

/** Also supports XHR and callers that preserve a domain-specific error type. */
export function assertApiSuccess<T = unknown>(
  response: Pick<Response, "ok" | "status">,
  payload: unknown,
  invalidMessage = "Invalid API response",
): ApiSuccess<T> {
  if (response.ok && payload !== null && typeof payload === "object" &&
      !Array.isArray(payload) && "status" in payload && payload.status === "success" &&
      (!("message" in payload) || typeof payload.message === "string")) {
    return payload as ApiSuccess<T>;
  }
  const message = payload !== null && typeof payload === "object" &&
    !Array.isArray(payload) && "message" in payload &&
    typeof payload.message === "string" && payload.message.trim()
      ? payload.message : response.ok ? invalidMessage : `HTTP ${response.status}`;
  throw new Error(localizeServerError(payload, message, i18n));
}
