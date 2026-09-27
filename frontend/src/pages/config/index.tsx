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
    (includeSecrets: boolean) =>
      call<{ include_secrets: boolean }, Record<string, unknown>>("admin:exportConfig", {
        include_secrets: includeSecrets,
      }),
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