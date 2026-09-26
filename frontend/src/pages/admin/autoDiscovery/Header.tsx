import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button, Dialog, Flex, Text, TextField } from "@radix-ui/themes";
import { Plus } from "lucide-react";
import { toast } from "sonner";
import { useNodeDetails } from "@/contexts/NodeDetailsContext";
import { AutoDiscoverySection } from "./AutoDiscoverySection";
import { requireClientMutationSuccess } from "../nodeTable/mutationResult";

/**
 * The node list's title bar: the search box, the selected count, and the
 * "add node" dialog that `AutoDiscoverySection` fills.
 */
export const Header = ({
  searchTerm,
  setSearchTerm,
  selectedNodes,
  settings,
  settingsLoading,
}: {
  searchTerm: string;
  setSearchTerm: (term: string) => void;
  selectedNodes: string[];
  settings: any;
  settingsLoading: boolean;
}) => {
  const { t } = useTranslation();
  const { refresh } = useNodeDetails();
  const [loading, setLoading] = useState(false);
  const [dialogOpen, setDialogOpen] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const handleAddNode = async (name: string | undefined) => {
    setLoading(true);
    try {
      const response = await fetch("/api/admin/client/add", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: name || "" }),
      });
      await requireClientMutationSuccess(response);
      refresh();
      setDialogOpen(false);
    } catch (error) {
      toast.error(
        `${t("common.error", "Error")}: ${
          error instanceof Error ? error.message : String(error)
        }`
      );
    } finally {
      setLoading(false);
    }
  };
  return (
    <Flex justify="between" align="center" gap="4" wrap="wrap">
      <Flex gap="2" align="center">
        <Text size="5" weight="bold">
          {t("admin.nodeTable.nodeList")}
        </Text>
        {selectedNodes.length > 0 && (
          <Text size="2" data-testid="selected-count">
            ({selectedNodes.length} selected)
          </Text>
        )}
      </Flex>
      <Flex gap="2">
        <TextField.Root
          placeholder={t("admin.nodeTable.searchByName")}
          value={searchTerm}
          onChange={(e) => setSearchTerm(e.target.value)}
        />
        <Dialog.Root open={dialogOpen} onOpenChange={setDialogOpen}>
          <Dialog.Trigger>
            <Button onClick={() => setDialogOpen(true)}>
              <Plus size={16} />
              {t("admin.nodeTable.addNode")}
            </Button>
          </Dialog.Trigger>
          <Dialog.Content>
            <Dialog.Title>{t("admin.nodeTable.addNode")}</Dialog.Title>
            <TextField.Root
              ref={inputRef}
              placeholder={t("admin.nodeTable.nameOptional")}
            />
            <Flex justify="end" gap="2" mt="4">
              <Button
                onClick={() => handleAddNode(inputRef.current?.value)}
                disabled={loading}
              >
                {t("admin.nodeTable.addNode")}
              </Button>
            </Flex>
            <AutoDiscoverySection
              settings={settings}
              loading={settingsLoading}
            />
          </Dialog.Content>
        </Dialog.Root>
      </Flex>
    </Flex>
  );
};
