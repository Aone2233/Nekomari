import { useTranslation } from "react-i18next";
import { Button, IconButton } from "@radix-ui/themes";
import { Link } from "react-router-dom";
import { Terminal } from "lucide-react";
import type { NodeDetail } from "@/contexts/NodeDetailsContext";
import { GenerateCommandButton } from "./GenerateCommandButton";
import { BillingButton, DeleteButton, EditButton } from "./NodeDialogs";
import { RotateTokenButton } from "./RotateTokenButton";

/** The per-row action strip: install command, terminal, edit, billing, delete. */
export const ActionButtons = ({
  node,
  settings,
}: {
  node: NodeDetail;
  settings: any;
}) => {
  const { t } = useTranslation();
  return (
    <div className="flex items-center gap-4">
      <Button asChild size="1" variant="soft">
        <Link to={`/admin/servers/${encodeURIComponent(node.uuid)}/charts`}>{t("chart.historyExport")}</Link>
      </Button>
      <GenerateCommandButton
        node={node}
        settings={settings}
      />
      <IconButton
        title={t("terminal.title")}
        variant="ghost"
        onClick={() => {
          window.open(`/terminal?uuid=${node.uuid}`, "_blank");
        }}
      >
        <Terminal size="18" />
      </IconButton>
      <EditButton node={node} />
      <RotateTokenButton node={node} />
      <BillingButton node={node} />
      <DeleteButton node={node} />
    </div>
  );
};
