import { useEffect, useState } from "react";
import { useNodeDetails } from "@/contexts/NodeDetailsContext";
import { NodeDetailsProvider } from "@/contexts/NodeDetailsProvider";
import { Flex } from "@radix-ui/themes";
import Loading from "@/components/loading";
import { useSettings } from "@/lib/api";
import { NodeTable } from "./nodeTable/NodeTable";
import { EmptyNodesGuide } from "./autoDiscovery/EmptyNodesGuide";
import { Header } from "./autoDiscovery/Header";

/**
 * The admin node list.
 *
 * This file used to be 3055 lines and held the whole page: the table, the
 * dialogs behind each row, the two install-command generators, the auto-discovery
 * panel and the header. Roadmap A1 called it the single worst compound-risk item,
 * so a mounted fixture came first (`script/admin-node-table.browser.spec.py`,
 * which mounts `Layout` below) and then the pieces moved out — see the
 * `nodeTable/` and `autoDiscovery/` directories.
 *
 * What is left is the wiring only this page owns: the node-list provider, the 5 s
 * poll, the search filter, the weight sort, the selection, and the choice between
 * the empty-state guide and the table.
 */
const NodeDetailsPage = () => {
  return (
    <NodeDetailsProvider>
      <Layout />
    </NodeDetailsProvider>
  );
};

// Exported for the mounted regression in script/admin-node-table.browser.tsx.
// The page owns the state the fixture has to observe — the poll interval, the
// search filter, the sort, and the selection — and NodeTable owns only part of
// it, so mounting NodeTable alone would test the wrong wiring. Exporting the
// component is a visibility change: no behaviour depends on it.
export const Layout = () => {
  const { nodeDetail, isLoading, error, refresh } = useNodeDetails();
  const { settings, loading: settingsLoading } = useSettings();
  const [searchTerm, setSearchTerm] = useState("");
  const [selectedNodes, setSelectedNodes] = useState<string[]>([]);
  const filteredNodes = Array.isArray(nodeDetail)
    ? nodeDetail
        .filter((node) =>
          node.name.toLowerCase().includes(searchTerm.toLowerCase())
        )
        .sort((a, b) => a.weight - b.weight)
    : [];

  useEffect(() => {
    const interval = setInterval(() => { refresh() }, 5000);
    return () => clearInterval(interval);
    // 只依赖 refresh（NodeDetailsProvider 里已用 useCallback 固定引用）：
    // 之前依赖 nodeDetail 会让每次轮询响应都拆掉旧定时器、重建新定时器，
    // 请求频率没变但定时器被无限重建；现在每个挂载周期恰好一个 interval。
  }, [refresh]);

  if (isLoading) return <Loading text="" />;
  if (error) return <div>{error}</div>;

  const isEmpty = Array.isArray(nodeDetail) && nodeDetail.length === 0;

  return (
    <Flex direction="column" gap="4" p="4" className="km-page-admin-index">
      <Header
        searchTerm={searchTerm}
        setSearchTerm={setSearchTerm}
        selectedNodes={selectedNodes}
        settings={settings}
        settingsLoading={settingsLoading}
      />

      {isEmpty ? (
        <EmptyNodesGuide />
      ) : (
        <NodeTable
          nodes={filteredNodes}
          selectedNodes={selectedNodes}
          setSelectedNodes={setSelectedNodes}
          settings={settings}
        />
      )}
    </Flex>
  );
};

export default NodeDetailsPage;
