import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Checkbox, Flex, IconButton, Text } from "@radix-ui/themes";
import { Copy, MenuIcon } from "lucide-react";
import {
  DndContext,
  closestCenter,
  useSensor,
  useSensors,
  TouchSensor,
  MouseSensor,
  KeyboardSensor,
} from "@dnd-kit/core";
import {
  SortableContext,
  useSortable,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { toast } from "sonner";
import type { NodeDetail } from "@/contexts/NodeDetailsContext";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useIsMobile } from "@/hooks/use-mobile";
import PriceTags from "@/components/PriceTags";
import { IcmpCapabilityBadge } from "@/components/admin/IcmpCapabilityBadge";
import { ActionButtons } from "./NodeActions";
import { DetailView } from "./NodeDialogs";
import { requireClientMutationSuccess } from "./mutationResult";
import { useIsSnapshotBackend } from "./useIsSnapshotBackend";

const SortableRow = ({
  node,
  selectedNodes,
  handleSelectNode,
  settings,
  isSnapshotBackend,
}: {
  node: NodeDetail;
  selectedNodes: string[];
  handleSelectNode: (uuid: string, checked: boolean) => void;
  settings: any;
  isSnapshotBackend: boolean;
}) => {
  const { attributes, listeners, setNodeRef, transform, transition } =
    useSortable({ id: node.uuid });
  const { t } = useTranslation();
  const isMobile = useIsMobile();
  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
  };
  function copy(text: string) {
    navigator.clipboard.writeText(text);
    toast.success(t("copy_success"));
  }
  return (
    <TableRow
      ref={setNodeRef}
      style={style}
      className="hover:bg-accent-a2"
      // The mounted fixture reads row identity and order from here rather than
      // from the rendered text, which repeats the name in the drawer and the flag.
      data-testid="node-row"
      data-node-uuid={node.uuid}
    >
      <TableCell>
        <div
          {...attributes}
          {...listeners}
          className={`cursor-move p-2 rounded hover:bg-accent-a3 transition-colors ${
            isMobile ? "touch-manipulation select-none" : ""
          }`}
          style={{
            touchAction: "none", // 禁用移动端的默认手势
            WebkitUserSelect: "none",
            userSelect: "none",
          }}
          title={
            isMobile
              ? t("admin.nodeTable.dragToReorder", "长按拖拽重新排序")
              : undefined
          }
        >
          <MenuIcon size={isMobile ? 18 : 16} color={"var(--gray-8)"} />
        </div>
      </TableCell>
      <TableCell>
        <Checkbox
          checked={selectedNodes.includes(node.uuid)}
          onCheckedChange={(checked) => handleSelectNode(node.uuid, !!checked)}
        />
      </TableCell>
      <TableCell>
        <DetailView node={node} />
      </TableCell>
      <TableCell>
        <Flex direction="column">
          {node.ipv4 && (
            <Text size="2" className="flex items-center gap-1">
              {node.ipv4}
              <IconButton variant="ghost" onClick={() => copy(node.ipv4)}>
                <Copy size="16" />
              </IconButton>
            </Text>
          )}
          {node.ipv6 && (
            <Text
              size="2"
              className="flex items-center gap-1"
              title={node.ipv6}
            >
              {node.ipv6.length > 20
                ? (() => {
                    const segments = node.ipv6.split(":");
                    return segments.length > 3
                      ? `${segments.slice(0, 2).join(":")}:...${
                          segments[segments.length - 1]
                        }`
                      : node.ipv6;
                  })()
                : node.ipv6}
              <IconButton variant="ghost" onClick={() => copy(node.ipv6)}>
                <Copy size="16" />
              </IconButton>
            </Text>
          )}
        </Flex>
      </TableCell>
      <TableCell>
        <Flex align="center" gap="2">
          <Text size="2">{node.version}</Text>
          {/* The node's own report about ICMP, beside the version because both
              are facts about the agent rather than about the target. */}
          <IcmpCapabilityBadge capability={node.icmp_capability} compact />
        </Flex>
      </TableCell>
      <TableCell>
        <Text
          size="2"
          title={node.group}
          style={{
            maxWidth: "150px",
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
        >
          {node.group && node.group.length > 10
            ? `${node.group.slice(0, 10)}...`
            : node.group}
        </Text>
      </TableCell>
      <TableCell>
        <Text
          size="2"
          title={node.remark}
          style={{
            maxWidth: "150px",
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
        >
          {node.remark && node.remark.length > 10
            ? `${node.remark.slice(0, 10)}...`
            : node.remark}
        </Text>
      </TableCell>
      <TableCell>
        <PriceTags
          price={node.price}
          billing_cycle={node.billing_cycle}
          expired_at={node.expired_at}
          currency={node.currency}
          tags={node.tags || ""}
        />
      </TableCell>
      <TableCell>
        <ActionButtons
          node={node}
          settings={settings}
          isSnapshotBackend={isSnapshotBackend}
        />
      </TableCell>
    </TableRow>
  );
};

export const NodeTable = ({
  nodes,
  selectedNodes,
  setSelectedNodes,
  settings,
}: {
  nodes: NodeDetail[];
  selectedNodes: string[];
  setSelectedNodes: (nodes: string[]) => void;
  settings: any;
}) => {
  const { t } = useTranslation();
  const sensors = useSensors(
    useSensor(MouseSensor, {
      // 需要按住 10px 距离才开始拖拽，避免与点击冲突
      activationConstraint: {
        distance: 10,
      },
    }),
    useSensor(TouchSensor, {
      // 移动端需要按住 5px 距离才开始拖拽，并且延迟 200ms，避免与滚动冲突
      activationConstraint: {
        delay: 200,
        tolerance: 5,
      },
    }),
    useSensor(KeyboardSensor, {})
  );
  // 添加 localNodes 状态，实现即时 UI 更新
  const [localNodes, setLocalNodes] = useState<NodeDetail[]>(nodes);
  const [isDragging, setIsDragging] = useState(false);
  const isSnapshotBackend = useIsSnapshotBackend();
  // 与原先 `useEffect(..., [nodes])` 等价：nodes 引用变化时在渲染期间重新同步一次。
  const [syncedNodes, setSyncedNodes] = useState(nodes);
  if (syncedNodes !== nodes) {
    setSyncedNodes(nodes);
    setLocalNodes(nodes);
  }
  const handleDragStart = () => {
    setIsDragging(true);
    if ("vibrate" in navigator) {
      navigator.vibrate(50);
    }
  };

  const handleDragEnd = async (event: any) => {
    setIsDragging(false);
    const { active, over } = event;
    if (!over || active.id === over.id) return;

    const oldIndex = localNodes.findIndex((node) => node.uuid === active.id);
    const newIndex = localNodes.findIndex((node) => node.uuid === over.id);
    if (oldIndex < 0 || newIndex < 0) return;
    const reorderedNodes = Array.from(localNodes);
    const [reorderedItem] = reorderedNodes.splice(oldIndex, 1);
    reorderedNodes.splice(newIndex, 0, reorderedItem);

    // 立即更新 UI
    setLocalNodes(reorderedNodes);

    if ("vibrate" in navigator) {
      navigator.vibrate([30, 10, 30]);
    }

    try {
      const orderData = reorderedNodes.reduce((acc, node, index) => {
        acc[node.uuid] = index;
        return acc;
      }, {} as Record<string, number>);

      const response = await fetch("/api/admin/client/order", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(orderData),
      });
      await requireClientMutationSuccess(response);
      // 不再调用 refresh，以免覆盖本地排序
    } catch {
      setLocalNodes((current) => current === reorderedNodes ? localNodes : current);
      toast.error(t("admin.nodeTable.errorRefreshNodeList"));
    }
  };

  // 更新全选逻辑，使用 localNodes
  const handleSelectAll = (checked: boolean) => {
    setSelectedNodes(checked ? localNodes.map((node) => node.uuid) : []);
  };

  const handleSelectNode = (uuid: string, checked: boolean) => {
    setSelectedNodes(
      checked
        ? [...selectedNodes, uuid]
        : selectedNodes.filter((id) => id !== uuid)
    );
  };
  return (
    <div
      data-testid="node-table"
      // The rendered order, so a fixture can assert the sort and the local
      // reorder without reading the DOM's text and guessing at row identity.
      data-node-order={localNodes.map((node) => node.uuid).join(",")}
      className={`rounded-md overflow-hidden ${
        isDragging ? "select-none" : ""
      }`}
    >
      <DndContext
        sensors={sensors}
        collisionDetection={closestCenter}
        onDragStart={handleDragStart}
        onDragEnd={handleDragEnd}
      >
        <Table>
          <TableHeader style={{ backgroundColor: "var(--accent-4)" }}>
            <TableRow>
              <TableHead></TableHead>
              <TableHead>
                <Checkbox
                  data-testid="select-all"
                  checked={
                    selectedNodes.length === localNodes.length &&
                    localNodes.length > 0
                  }
                  onCheckedChange={handleSelectAll}
                />
              </TableHead>
              <TableHead>{t("admin.nodeTable.name")}</TableHead>
              <TableHead>{t("admin.nodeDetail.ipAddress")}</TableHead>
              <TableHead>{t("admin.nodeDetail.clientVersion")}</TableHead>
              <TableHead>{t("common.group")}</TableHead>
              <TableHead>{t("admin.nodeEdit.remark")}</TableHead>
              <TableHead>{t("admin.nodeTable.billing")}</TableHead>
              <TableHead></TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <SortableContext
              items={localNodes.map((node) => node.uuid)}
              strategy={verticalListSortingStrategy}
            >
              {localNodes.map((node) => (
                <SortableRow
                  key={node.uuid}
                  node={node}
                  selectedNodes={selectedNodes}
                  handleSelectNode={handleSelectNode}
                  settings={settings}
                  isSnapshotBackend={isSnapshotBackend}
                />
              ))}
            </SortableContext>
          </TableBody>
        </Table>
      </DndContext>
    </div>
  );
};
