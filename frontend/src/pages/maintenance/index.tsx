import React, { useCallback, useEffect, useState } from "react";
import { MaintenancePage, type MaintenanceWindow } from "@/components/maintenance/MaintenancePage";
import { useRPC2Call } from "@/contexts/RPC2Context";

/**
 * The maintenance window page shell (roadmap H3).
 *
 * Owns the reads and writes; the component owns the presentation and takes both as props, which is
 * what lets the fixture put an open window, a scoped one and a refused save on screen without a
 * server.
 *
 * `open` and `remaining_seconds` come from the server rather than a client-side date comparison:
 * recomputing them here would be a second definition of "open", and the one that matters is the one
 * the notifier uses.
 */
const MaintenanceShell: React.FC = () => {
  const { call } = useRPC2Call();
  const [windows, setWindows] = useState<MaintenanceWindow[]>([]);
  const [nodes, setNodes] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);

  const reload = useCallback(async () => {
    try {
      const result = await call<Record<string, never>, { windows: MaintenanceWindow[] }>(
        "admin:listMaintenanceWindows",
        {},
      );
      setWindows(result?.windows ?? []);
    } finally {
      setLoading(false);
    }
  }, [call]);

  useEffect(() => {
    void reload();
    // The scope picker needs the fleet. A failure here is not worth blocking the page for: the
    // scope select simply offers fleet-wide only, which is the common case.
    void call<Record<string, never>, Array<{ uuid: string }>>("admin:listClients", {}).then(
      (clients) => setNodes((clients ?? []).map((client) => client.uuid)),
      () => setNodes([]),
    );
  }, [call, reload]);

  const save = useCallback(
    async (window: {
      id: number;
      name: string;
      start: string;
      end: string;
      clients: string[];
      reason: string;
    }) => {
      await call("admin:saveMaintenanceWindow", window);
      await reload();
    },
    [call, reload],
  );

  const remove = useCallback(
    async (id: number) => {
      await call("admin:deleteMaintenanceWindow", { id });
      await reload();
    },
    [call, reload],
  );

  return (
    <MaintenancePage
      windows={windows}
      save={save}
      remove={remove}
      nodeUUIDs={nodes}
      loading={loading}
    />
  );
};

export default MaintenanceShell;
export { MaintenanceShell };