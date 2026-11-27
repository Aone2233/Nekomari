import { lazy, Suspense, useState } from "react";
import { Dialog, IconButton } from "@radix-ui/themes";
import { Download } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { InstallCommandProps } from "./InstallCommandContent";

const InstallCommandContent = lazy(() => import("./InstallCommandContent"));

export function GenerateCommandButton(props: InstallCommandProps) {
  const { t } = useTranslation();
  const [hasOpened, setHasOpened] = useState(false);
  return (
    <Dialog.Root onOpenChange={(open) => { if (open) setHasOpened(true); }}>
      <Dialog.Trigger>
        <IconButton variant="ghost" title={t("admin.nodeTable.installCommand")}>
          <Download size="18" />
        </IconButton>
      </Dialog.Trigger>
      {hasOpened && (
        <Suspense fallback={
          <Dialog.Content><Dialog.Title>{t("admin.nodeTable.installCommand")}</Dialog.Title><p role="status">{t("loading")}</p></Dialog.Content>
        }>
          <InstallCommandContent {...props} />
        </Suspense>
      )}
    </Dialog.Root>
  );
}
