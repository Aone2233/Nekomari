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
      // Text keeps large integer fields intact across JavaScript JSON parsing.
      // The credential-carrying method is separately sensitive-gated and uses
      // a fresh second factor per request, including over WebSocket.
      if (!includeSecrets) {
        return call<Record<string, never>, string>(
          "admin:exportConfigText",
        );
      }
      // An empty field sends no key at all: the account may have no 2FA, in which case an empty
      // string is not a code the server should try to verify.
      const code = twoFactorCode?.trim() ?? "";
      return call<{ "2fa_code"?: string }, string>(
        "admin:exportConfigSecretsText",
        code ? { "2fa_code": code } : {},
      );
    },
    [call],
  );

  const planImport = useCallback(
    (documentText: string) =>
      call<{ document_json: string }, ImportPlan>("admin:planConfigImport", { document_json: documentText }),
    [call],
  );

  const importConfig = useCallback(
    (documentText: string, reviewToken: string, twoFactorCode?: string) => {
      const code = twoFactorCode?.trim() ?? "";
      return call<{ document_json: string; review_token: string; "2fa_code"?: string }, ImportPlan>(
        "admin:importConfig",
        { document_json: documentText, review_token: reviewToken, ...(code ? { "2fa_code": code } : {}) },
      );
    },
    [call],
  );

  return (
    <ConfigExportPage importAvailable exportConfig={exportConfig} planImport={planImport} importConfig={importConfig} />
  );
};

export default ConfigShell;
export { ConfigShell };
