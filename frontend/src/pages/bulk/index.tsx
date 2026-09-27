import { Flex, Text } from "@radix-ui/themes";
import React, { useCallback } from "react";
import { BulkEditPage } from "@/components/bulk/BulkEditPage";
import { useNodeList } from "@/contexts/NodeListContext";
import { useRPC2Call } from "@/contexts/RPC2Context";
import type { BulkReport } from "@/types/Bulk";

/**
 * The bulk edit page shell (roadmap H2).
 *
 * Owns the two things the page component should not: where the node list comes from, and how
 * the edit is sent. `BulkEditPage` takes both as props, which is what lets the fixture put a
 * partial failure on screen without a server.
 *
 * The node list is the panel's own `NodeListProvider`, so it polls on the same cadence as the
 * rest of the UI and a node added elsewhere shows up here without a reload. The apply call is
 * `admin:bulkEditClients`, which applies each update through the same validation a
 * single-node edit uses and answers with one outcome per node — a partial failure is the
 * normal case this page is built to show, not an error path.
 */
const BulkPage: React.FC = () => {
  const { nodeList, isLoading, refresh } = useNodeList();
  const { call } = useRPC2Call();

  const apply = useCallback(
    async (uuids: string[], update: Record<string, unknown>): Promise<BulkReport> => {
      const report = await call<
        { uuids: string[]; update: Record<string, unknown> },
        BulkReport
      >("admin:bulkEditClients", { uuids, update });
      // Re-read the list rather than patching it locally: the panel is the authority on what
      // was stored, and a local edit would show the operator their own request rather than
      // the result. This is also what makes a *partial* failure legible — the rows that
      // changed change, and the ones that did not stay as they were.
      refresh();
      return report;
    },
    [call, refresh],
  );

  const nodes = React.useMemo(
    () =>
      (nodeList ?? []).map((node) => ({
        uuid: node.uuid,
        name: node.name,
        group: node.group,
        weight: node.weight,
        hidden: false,
      })),
    [nodeList],
  );

  return (
    <Flex direction="column" gap="2">
      <BulkEditPage nodes={nodes} apply={apply} loading={isLoading} />
      <Flex px="4" pb="4">
        <Text size="1" color="gray" data-testid="bulk-node-count">
          {nodes.length} nodes in the list
        </Text>
      </Flex>
    </Flex>
  );
};

export default BulkPage;
export { BulkPage };
