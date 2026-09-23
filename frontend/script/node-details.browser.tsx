import { createRoot } from "react-dom/client";
import { NodeDetailsProvider } from "../src/contexts/NodeDetailsProvider";
import { useNodeDetails } from "../src/contexts/NodeDetailsContext";

export function NodeDetailsFixture() {
  const { nodeDetail, isLoading, error, refresh } = useNodeDetails();
  return (
    <>
      <button type="button" onClick={refresh}>Refresh nodes</button>
      <pre data-testid="node-state">
        {JSON.stringify({ nodeDetail, isLoading, error })}
      </pre>
    </>
  );
}

createRoot(document.getElementById("root")!).render(
  <NodeDetailsProvider>
    <NodeDetailsFixture />
  </NodeDetailsProvider>,
);
