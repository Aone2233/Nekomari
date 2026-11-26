import { Link, useParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useAccount } from "@/contexts/AccountContext";
import { useNodeList } from "@/contexts/NodeListContext";
import LoadChart from "../instance/LoadChart";

export default function NodeCharts() {
  const { t } = useTranslation();
  const { uuid } = useParams<{ uuid: string }>();
  const { account, loading, error: accountError } = useAccount();
  const { nodeList, isLoading, error } = useNodeList();
  if (loading) return <p role="status">{t("loading")}</p>;
  if (accountError || !account?.logged_in) return <p>{t("login.desc")}</p>;
  if (isLoading) return <p role="status">{t("loading")}</p>;
  const node = nodeList?.find((item) => item.uuid === uuid);
  return <section className="min-w-0 space-y-4 p-4">
    <Link to="/admin/servers" className="underline">{t("common.serverList")}</Link>
    {error || !node ? <p role="alert">{t("chart.nodeUnavailable")}</p> : <>
      <h1 className="break-words text-xl font-semibold">{node.name} · {t("chart.historyExport")}</h1>
      <LoadChart key={node.uuid} historyOnly />
    </>}
  </section>;
}
