import { useEffect, useState } from "react";
import { useRPC2Call } from "@/contexts/RPC2Context";

/**
 * Whether the backend is the snapshot build, which changes what "install" means
 * for a node: a snapshot has no releases to pin to.
 *
 * It lives in its own module rather than beside the components that use it, for
 * the reason the lint rule gives: a file that exports a hook and a component
 * breaks fast refresh. It was `useIsSnapshotBackend` in `pages/admin/index.tsx`
 * before the node table was split out.
 */
export function useIsSnapshotBackend() {
  const { call } = useRPC2Call();
  const [isSnapshotBackend, setIsSnapshotBackend] = useState(false);

  useEffect(() => {
    let cancelled = false;

    call<unknown, { version?: string }>("common:getVersion")
      .then((info) => {
        if (!cancelled) {
          const version = info?.version?.trim().toLowerCase() || "";
          setIsSnapshotBackend(version.startsWith("snapshot"));
        }
      })
      .catch((error) => {
        console.error("Failed to fetch backend version:", error);
      });

    return () => {
      cancelled = true;
    };
  }, [call]);

  return isSnapshotBackend;
}
