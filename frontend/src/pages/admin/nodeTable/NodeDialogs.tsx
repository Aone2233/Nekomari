import { lazy, Suspense, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button, Dialog, Flex, IconButton } from "@radix-ui/themes";
import { CircleDollarSign, Pencil, Trash2Icon } from "lucide-react";
import { toast } from "sonner";
import Flag from "@/components/Flag";
import { useNodeDetails, type NodeDetail } from "@/contexts/NodeDetailsContext";
import { requireClientMutationSuccess } from "./mutationResult";

const DetailsDrawer = lazy(() => import("./NodeDialogContent").then(module => ({ default: module.DetailsDrawer })));
const EditContent = lazy(() => import("./NodeDialogContent").then(module => ({ default: module.EditContent })));
const BillingContent = lazy(() => import("./NodeDialogContent").then(module => ({ default: module.BillingContent })));

export function DetailView({ node }: { node: NodeDetail }) {
  const [opened, setOpened] = useState<boolean | null>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const { t } = useTranslation();
  return <>
    <button ref={trigger} type="button" aria-haspopup="dialog" aria-expanded={opened === true}
      className="h-8 flex items-center hover:underline cursor-pointer font-bold text-base" onClick={() => setOpened(true)}>
      <Flag flag={node.region} size="6" />
      {node.name.length > 25 ? node.name.slice(0, 25) + "..." : node.name}
    </button>
    {opened !== null && <Suspense fallback={<span role="status">{t("loading")}</span>}>
      <DetailsDrawer node={node} open={opened} onOpenChange={setOpened} returnFocus={() => trigger.current?.focus()} />
    </Suspense>}
  </>;
}

function NodeFormButton({ node, billing }: { node: NodeDetail; billing: boolean }) {
  const [open, setOpen] = useState(false);
  const { t } = useTranslation();
  const title = billing ? t("admin.nodeTable.billing", "账单") : t("admin.nodeEdit.editInfo", "编辑信息");
  const Content = billing ? BillingContent : EditContent;
  return <Dialog.Root open={open} onOpenChange={setOpen}>
    <Dialog.Trigger>
      <IconButton variant="ghost" title={title}>{billing ? <CircleDollarSign size="18" /> : <Pencil size="18" />}</IconButton>
    </Dialog.Trigger>
    <Dialog.Content>
      <Dialog.Title>{title}</Dialog.Title>
      <Suspense fallback={<p role="status">{t("loading")}</p>}>
        <Content node={node} onSaved={() => setOpen(false)} />
      </Suspense>
    </Dialog.Content>
  </Dialog.Root>;
}

export function EditButton({ node }: { node: NodeDetail }) {
  return <NodeFormButton node={node} billing={false} />;
}

export function BillingButton({ node }: { node: NodeDetail }) {
  return <NodeFormButton node={node} billing />;
}

export function DeleteButton({ node }: { node: NodeDetail }) {
  const { t } = useTranslation();
  const { refresh } = useNodeDetails();
  const [open, setOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const handleDelete = async () => {
    try {
      setDeleting(true);
      const response = await fetch(`/api/admin/client/${node.uuid}/remove`, { method: "POST" });
      await requireClientMutationSuccess(response);
      toast.success(`Delete ${node.name}`);
      setOpen(false);
      refresh();
    } catch (error) {
      toast.error(`Error: ${error instanceof Error ? error.message : String(error)}`);
    } finally {
      setDeleting(false);
    }
  };
  return (
    <Dialog.Root open={open} onOpenChange={setOpen}>
      <Dialog.Trigger>
        <IconButton variant="ghost" color="red" title={t("common.delete")}><Trash2Icon size="18" /></IconButton>
      </Dialog.Trigger>
      <Dialog.Content>
        <Dialog.Title>{t("common.delete")}</Dialog.Title>
        <Dialog.Description>{t("common.confirm_delete")}</Dialog.Description>
        <Flex justify="end" gap="2" mt="4">
          <Dialog.Close><Button variant="soft">{t("common.cancel")}</Button></Dialog.Close>
          <Button disabled={deleting} color="red" onClick={handleDelete}>{t("common.confirm_delete")}</Button>
        </Flex>
      </Dialog.Content>
    </Dialog.Root>
  );
}
