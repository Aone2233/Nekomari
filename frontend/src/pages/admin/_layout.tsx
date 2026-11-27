import { Outlet } from "react-router-dom";

import AdminPanelBar from "../../components/admin/AdminPanelBar";
import { AdminNavigationProvider } from "@/contexts/AdminNavigationProvider";
import { updateSettingsWithToast, useSettings } from "@/lib/api";
import { Dialog } from "@radix-ui/themes";
import { lazy, Suspense, useState } from "react";
import { normalizeLanguage, readStoredLanguage } from "@/utils/language";
import { useTranslation } from "react-i18next";
const EulaContent = lazy(() => import("@/components/admin/EulaContent"));

const AdminLayout = () => {
  const { t } = useTranslation();
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
          <Dialog.Title>{t("eula.title")}</Dialog.Title>
          <Suspense fallback={<p role="status">{t("loading")}</p>}>
            <EulaContent onAccept={async () => {
              try {
                await updateSettingsWithToast({ eula_accepted: true }, t);
                setSettings((prev) => ({ ...prev, eula_accepted: true }));
                setOpen(false);
              } catch {
                setOpen(true);
              }
            }} />
          </Suspense>
        </Dialog.Content>
      </Dialog.Root>
      <AdminNavigationProvider>
        <AdminPanelBar content={<Outlet />} />
      </AdminNavigationProvider>
    </>
  );
};

export default AdminLayout;
