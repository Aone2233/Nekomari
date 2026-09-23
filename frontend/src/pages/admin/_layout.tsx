import { Outlet } from "react-router-dom";

import AdminPanelBar from "../../components/admin/AdminPanelBar";
import { AdminNavigationProvider } from "@/contexts/AdminNavigationProvider";
import { AccountProvider } from "@/contexts/AccountProvider";
import { updateSettingsWithToast, useSettings } from "@/lib/api";
import { Button, Dialog } from "@radix-ui/themes";
import { useState } from "react";
import { getEula } from "@/utils/eula";
import { normalizeLanguage, readStoredLanguage } from "@/utils/language";
import { useTranslation } from "react-i18next";
const AdminLayout = () => {
  const { t, i18n } = useTranslation();
  const { settings, loading, error, setSettings } = useSettings();
  const lang = readStoredLanguage() || "en";
  const [open, setOpen] = useState(false);
  // 与原先 `useEffect(..., [loading, error, settings, lang])` 等价：任一依赖变化时
  // 在渲染期间按同一条件同步。守卫只在这些依赖变化时生效，因此“关闭后不再自动重开”
  // 的闩锁语义保持不变。
  // 哨兵从 null 起步，使挂载时也执行一次——原 effect 在挂载时是可能 setOpen(true) 的
  // （settings 已加载、EULA 未接受且语言为中文），跳过它会让该弹窗不再出现。
  const [syncedEulaInputs, setSyncedEulaInputs] = useState<{
    loading: boolean;
    error: unknown;
    settings: unknown;
    lang: string;
  } | null>(null);
  if (
    syncedEulaInputs === null ||
    syncedEulaInputs.loading !== loading ||
    syncedEulaInputs.error !== error ||
    syncedEulaInputs.settings !== settings ||
    syncedEulaInputs.lang !== lang
  ) {
    setSyncedEulaInputs({ loading, error, settings, lang });
    if (loading || error || !settings || settings.eula_accepted !== false) {
      setOpen(false);
    } else if (normalizeLanguage(lang).startsWith("zh")) {
      setOpen(true);
    }
  }
  return (
    <>
      <Dialog.Root open={open}>
        <Dialog.Content className="km-admin-eula-dialog">
          <Dialog.Content>
            <Dialog.Title>{t("eula.title")}</Dialog.Title>
            <div className="km-admin-eula-content flex flex-col gap-2">
              <div className="max-h-[70vh] overflow-y-auto space-y-4">
                <pre className="text-wrap">{getEula(i18n.language)}</pre>
              </div>
              <div className="flex flex-row gap-2 justify-end items-center">
                <Button
                  variant="soft"
                  color="red"
                  onClick={() => window.close()}
                >
                  {t("eula.reject")}
                </Button>
                <Button
                  variant="solid"
                  onClick={async () => {
                    try {
                      await updateSettingsWithToast(
                        { eula_accepted: true },
                        (key) => key
                      );
                      setSettings((prev) => ({
                        ...prev,
                        eula_accepted: true,
                      }));
                      setOpen(false);
                    } catch {
                      setOpen(true);
                    }
                  }}
                >
                  {t("eula.accept")}
                </Button>
              </div>
            </div>
          </Dialog.Content>
        </Dialog.Content>
      </Dialog.Root>
      <AccountProvider>
        <AdminNavigationProvider>
          <AdminPanelBar content={<Outlet />} />
        </AdminNavigationProvider>
      </AccountProvider>
    </>
  );
};

export default AdminLayout;
