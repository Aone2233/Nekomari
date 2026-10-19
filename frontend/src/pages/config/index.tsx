import React, { useCallback } from "react";
import { ConfigExportPage, type ImportPlan } from "@/components/config/ConfigExportPage";
import { useRPC2Call } from "@/contexts/RPC2Context";

/**
 * The configuration export/import page shell (roadmap H4).
 *
 * Owns the three RPCs; the component owns the flow and takes them as props, which is what lets the
 * fixture put a refusal, a plan with removals and a successful import on screen without a server.
 */
const ConfigShell: React.FC = () => {
  const { call } = useRPC2Call();

  const exportConfig = useCallback(
    (includeSecrets: boolean, twoFactorCode?: string) => {
      // Credentials live behind a separate method: admin:exportConfig refuses include_secrets
      // outright rather than quietly returning a document without the tokens the caller asked for.
      // admin:exportConfigSecrets is registered as a sensitive operation, so the transport checks a
      // second factor per request, and it reads that factor from params (2fa_code) — which is also
      // what keeps a long-lived WebSocket from reusing an expired code.
      if (!includeSecrets) {
        return call<Record<string, never>, Record<string, unknown>>(
          "admin:exportConfig",
        );
      }
      // An empty field sends no key at all: the account may have no 2FA, in which case an empty
      // string is not a code the server should try to verify.
      const code = twoFactorCode?.trim() ?? "";
      return call<{ "2fa_code"?: string }, Record<string, unknown>>(
        "admin:exportConfigSecrets",
        code ? { "2fa_code": code } : {},
      );
    },
    [call],
  );

  const planImport = useCallback(
    (document: Record<string, unknown>) =>
      call<{ document: Record<string, unknown> }, ImportPlan>("admin:planConfigImport", { document }),
    [call],
  );

  const importConfig = useCallback(
    (document: Record<string, unknown>) =>
      call<{ document: Record<string, unknown> }, ImportPlan>("admin:importConfig", { document }),
    [call],
  );

  return (
    <ConfigExportPage exportConfig={exportConfig} planImport={planImport} importConfig={importConfig} />
  );
};

export default ConfigShell;
export { ConfigShell };