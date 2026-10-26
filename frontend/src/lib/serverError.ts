import type { i18n } from "i18next";

export type ServerErrorTranslator = Pick<i18n, "exists" | "t">;

/** Translate explicit presentation metadata only; never guess from English text. */
export function localizeServerError(payload: unknown, fallback: string, translator: ServerErrorTranslator): string {
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) return fallback;
  const record = payload as Record<string, unknown>;
  const data = record.data;
  const key = record.error_key ?? (data && typeof data === "object" && !Array.isArray(data)
    ? (data as Record<string, unknown>).error_key : undefined);
  if (typeof key !== "string" || !/^[a-z][a-z0-9_]{0,63}$/.test(key)) return fallback;
  const path = `server_errors.${key}`;
  if (!translator.exists(path)) return fallback;
  const message = translator.t(path);
  return typeof message === "string" && message.trim() && message !== path ? message : fallback;
}
