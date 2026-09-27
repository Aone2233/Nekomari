/**
 * Helpers for the configuration export/import page (roadmap H4).
 *
 * In their own file because a module exporting both a component and plain functions breaks fast
 * refresh — the same reason `slaFormat.ts` and `maintenanceFormat.ts` sit outside their components,
 * and eslint enforces it.
 */

import type { ImportPlan } from "@/components/config/ConfigExportPage";

/** parseDocument turns the textarea's contents into an object, or explains why not. */
export const parseDocument = (
  text: string,
): { document?: Record<string, unknown>; error?: string } => {
  if (!text.trim()) return { error: "empty" };
  try {
    const parsed = JSON.parse(text);
    if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
      return { error: "not an object" };
    }
    return { document: parsed as Record<string, unknown> };
  } catch (cause) {
    return { error: cause instanceof Error ? cause.message : "invalid JSON" };
  }
};

/** planCounts summarises a plan in one line, in the order an operator reads them. */
export const planCounts = (plan: ImportPlan): string =>
  `${plan.creates} create, ${plan.updates} update, ${plan.unchanged} unchanged, ${plan.removals} not in the document`;