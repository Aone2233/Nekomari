import { lazy, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useAccount } from "@/contexts/AccountContext";
import { usePublicInfo } from "@/contexts/PublicInfoContext";
import Loading from "../loading";

const AdminLogin = lazy(() => import("../Login").then(({ AccountLoginDialog }) => ({ default: AccountLoginDialog })));

export function AdminSession({ children }: { children: ReactNode }) {
  const { account, loading, error, refresh } = useAccount();
  const { refresh: refreshPublicInfo } = usePublicInfo();
  const { t } = useTranslation();
  if (loading) return <Loading />;
  if (error || !account) return (
    <main className="grid min-h-screen place-content-center gap-3 text-center">
      <p role="alert">{t("common.error")}</p>
      <button type="button" onClick={refresh}>{t("common.retry")}</button>
    </main>
  );
  if (!account.logged_in) return (
    <main className="grid min-h-screen place-content-center">
      <AdminLogin autoOpen showSettings={false} onLoginSuccess={() => {
        void refreshPublicInfo();
        refresh();
      }} />
    </main>
  );
  return children;
}
