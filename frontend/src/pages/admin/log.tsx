import React from "react";
import {
  Table,
  TableHeader,
  TableBody,
  TableRow,
  TableHead,
  TableCell,
} from "@/components/ui/table";
import { Button, Dialog, Flex, Select } from "@radix-ui/themes";
import { Activity, Download } from "lucide-react";
import { useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import NumberPicker from "@/components/ui/number-picker";
import Loading from "@/components/loading";
import {
  ALL_LOG_TYPES,
  LOG_EXPORT_MAX_ROWS,
  LOG_EXPORT_PAGE_SIZE,
  LOG_MSG_TYPES,
  logsQuery,
  logsToCsv,
} from "./logCsv";

interface Log {
  id: number;
  ip: string;
  uuid: string;
  message: string;
  msg_type: string;
  time: string;
}
const LogPage = () => {
  const [loading, setLoading] = React.useState<boolean>(true);
  const [logs, setLogs] = React.useState<Log[]>([]);
  const [error, setError] = React.useState<string | null>(null);
  const [page, setPage] = React.useState<number>(1);
  const [total, setTotal] = React.useState<number>(1);
  const [limit, setLimit] = React.useState<number>(10);
  const [selectedType, setSelectedType] = React.useState<string>(ALL_LOG_TYPES);
  const [exporting, setExporting] = React.useState<boolean>(false);
  const [exportError, setExportError] = React.useState<string | null>(null);
  const [exportNotice, setExportNotice] = React.useState<string | null>(null);
  const [t] = useTranslation();
  const navigate = useNavigate();
  // Stable identity, so the picker sees one callback for the life of the page.
  const handleLimitChange = React.useCallback((value: number) => {
    setLimit(value);
    setPage(1);
  }, []);
  const handleTypeChange = React.useCallback((value: string) => {
    setSelectedType(value);
    // 换类型后旧页码可能已越过新结果的最后一页，表格会请求一个不存在的页并显示空表。
    setPage(1);
  }, []);
  React.useEffect(() => {
    const fetchLogs = async () => {
      setLoading(true);
      try {
        // 查询串和导出共用一个构造函数（见 logCsv.ts），筛选条件不会只对其中一边生效。
        const response = await fetch(logsQuery(limit, page, selectedType));
        if (!response.ok) {
          throw new Error("Failed to fetch logs");
        }
        const data = await response.json();
        setLogs(data.data.logs);
        setTotal(data.data.total);
      } catch (err) {
        setError(err instanceof Error ? err.message : "Unknown error");
      } finally {
        setLoading(false);
      }
    };
    fetchLogs();
    // limit 是真实依赖：URL 里带着它，之前漏掉意味着改 Limit 后表格不会重新拉取，
    // 而 totalPages 已经按新 limit 计算 —— 数据和分页会互相矛盾。
    // 请求次数：每次 limit 变化 +1 次（NumberPicker 每敲一个合法数字发一次
    // onChange，输入 "50" 会发 2 次请求；点 +/- 按钮发 1 次；失焦时值没变则
    // setState 同值 bail-out，不额外发请求）。挂载时不再多发一次请求：
    // picker 的 defaultValue 同步已不再回吐 onChange。
  }, [page, limit, selectedType]);

  // 导出走另一条取数路径：按当前筛选逐页拉到大表之外再落盘，不动表格里的 page/limit。
  // 上限 10000 行是页面上真正会打开的日志行数，不是数据库容量 —— 一次点击拉整张表会让
  // 面板在最忙的时候替一个人做一次全表扫描。
  const exportCsv = React.useCallback(async () => {
    setExporting(true);
    setExportError(null);
    setExportNotice(null);
    try {
      const rows: Log[] = [];
      let totalRows: number | null = null;
      let lastPageFull = false;
      const maxPages = Math.ceil(LOG_EXPORT_MAX_ROWS / LOG_EXPORT_PAGE_SIZE);
      for (let exportPage = 1; exportPage <= maxPages; exportPage++) {
        const response = await fetch(
          logsQuery(LOG_EXPORT_PAGE_SIZE, exportPage, selectedType),
        );
        if (!response.ok) throw new Error(`HTTP ${response.status}`);
        const payload = await response.json();
        const pageLogs: Log[] = Array.isArray(payload?.data?.logs)
          ? payload.data.logs
          : [];
        totalRows =
          typeof payload?.data?.total === "number" ? payload.data.total : null;
        rows.push(...pageLogs);
        lastPageFull = pageLogs.length === LOG_EXPORT_PAGE_SIZE;
        if (!lastPageFull) break; // 最后一页
        if (totalRows !== null && rows.length >= totalRows) break;
      }

      const exported = rows.slice(0, LOG_EXPORT_MAX_ROWS);
      // 截断提示只在“确实还有没导出的行”时出现：拿到了上限但 total 恰好等于上限，不算截断。
      const truncated =
        exported.length === LOG_EXPORT_MAX_ROWS &&
        (totalRows === null ? lastPageFull : totalRows > LOG_EXPORT_MAX_ROWS);

      const blob = new Blob([logsToCsv(exported)], {
        type: "text/csv;charset=utf-8",
      });
      const url = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = `logs-${selectedType === ALL_LOG_TYPES ? "all" : selectedType}.csv`;
      document.body.append(link);
      link.click();
      link.remove();
      window.setTimeout(() => URL.revokeObjectURL(url), 0);

      if (truncated) {
        setExportNotice(
          t("logs.export.truncated", {
            rows: LOG_EXPORT_MAX_ROWS,
            defaultValue: "Only the first {{rows}} rows were exported",
          }),
        );
      }
    } catch (cause) {
      setExportError(
        `${t("logs.export.failed", "Export failed")}: ${cause instanceof Error ? cause.message : String(cause)}`,
      );
    } finally {
      setExporting(false);
    }
  }, [selectedType, t]);

  const totalPages = Math.ceil(total / limit);
  // 计算分页页码，显示当前页及前后1页，两端省略号
  const siblingsCount = 1;
  let pageNumbers: (number | string)[] = [];
  const leftSibling = Math.max(page - siblingsCount, 1);
  const rightSibling = Math.min(page + siblingsCount, totalPages);
  const showLeftDots = leftSibling > 2;
  const showRightDots = rightSibling < totalPages - 1;
  // 始终包含第一页
  pageNumbers.push(1);
  // 左侧省略或中间连续页
  if (showLeftDots) {
    pageNumbers.push("...");
  } else {
    for (let i = 2; i < leftSibling; i++) pageNumbers.push(i);
  }
  // 中间页，仅当不重复首尾页时加入
  for (let i = leftSibling; i <= rightSibling; i++) {
    if (i > 1 && i < totalPages) pageNumbers.push(i);
  }
  // 右侧省略或中间连续页
  if (showRightDots) {
    pageNumbers.push("...");
  } else {
    for (let i = rightSibling + 1; i < totalPages; i++) pageNumbers.push(i);
  }
  // 始终包含最后一页（如果大于1）
  if (totalPages > 1) pageNumbers.push(totalPages);

  if (loading) {
    return <Loading />;
  }
  if (error) {
    return <div>Error: {error}</div>;
  }

  return (
    <div className="km-page-admin-log flex flex-col gap-2 p-4">
      <div className="km-log-toolbar flex justify-between items-center">
        <h1 className="text-2xl font-bold">{t("logs.title")}</h1>
        <div className="flex items-center gap-2 flex-wrap">
          {/* 类型筛选的值与后端写入的 msg_type 完全一致（见 logCsv.ts），"all" 不发给服务端。 */}
          <Select.Root value={selectedType} onValueChange={handleTypeChange}>
            <Select.Trigger
              aria-label={t("logs.filter.type", "Type")}
              data-testid="log-type-filter"
            />
            <Select.Content>
              <Select.Item value={ALL_LOG_TYPES}>
                {t("logs.filter.allTypes", "All types")}
              </Select.Item>
              {LOG_MSG_TYPES.map((type) => (
                <Select.Item key={type} value={type}>
                  {type}
                </Select.Item>
              ))}
            </Select.Content>
          </Select.Root>
          <Button variant="soft" onClick={() => navigate("/admin/pprof")}>
            <Activity size={16} />
            {t("pprof.title")}
          </Button>
          Limit
          <NumberPicker
            defaultValue={limit}
            // Changing the page size returns to the first page: the current page
            // can be past the new last page, and the table would then render
            // empty while the data it asked for exists on page 1.
            //
            // This callback is memoised because it is the picker's only inbound
            // contract: the picker must not re-run anything when the parent
            // re-renders, and a fresh inline arrow here used to make its
            // `defaultValue` effect re-fire on every render — which re-entered
            // this handler, so `setPage(1)` undid every page the user selected.
            onChange={handleLimitChange}
            min={1}
            max={100}
          />
          <Button
            variant="soft"
            onClick={() => void exportCsv()}
            disabled={exporting}
            loading={exporting}
            data-testid="log-export-csv"
          >
            <Download size={16} />
            {t("logs.export.csv", "Export CSV")}
          </Button>
        </div>
      </div>
      {(exportError || exportNotice) && (
        <div
          className="text-sm"
          role={exportError ? "alert" : "status"}
          data-testid="log-export-status"
        >
          {exportError ?? exportNotice}
        </div>
      )}
      <div className="km-log-output rounded-lg overflow-hidden">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>ID</TableHead>
              <TableHead>IP</TableHead>
              <TableHead>Type</TableHead>
              <TableHead>Message</TableHead>
              <TableHead>Time</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {logs.map((log) => (
              <TableRow key={log.id} data-log-id={log.id}>
                <TableCell>
                  <Dialog.Root>
                    <Dialog.Trigger>
                      <label className="hover:underline font-bold">
                        {log.id}
                      </label>
                    </Dialog.Trigger>
                    <Dialog.Content>
                      <Dialog.Title>{t("log.title")}</Dialog.Title>
                      <Flex direction="column" gap="1">
                        <label className="font-bold">ID</label>
                        <label className="text-sm">{log.id}</label>
                        <label className="font-bold">IP</label>
                        <label className="text-sm">{log.ip}</label>
                        <label className="font-bold">UUID</label>
                        <label className="text-sm">{log.uuid}</label>
                        <label className="font-bold">Type</label>
                        <label className="text-sm">{log.msg_type}</label>
                        <label className="font-bold">Message</label>
                        <label className="text-sm">{log.message}</label>
                        <label className="font-bold">Time</label>
                        <label className="text-sm">
                          {new Date(log.time).toLocaleString()}
                        </label>
                      </Flex>
                      <Flex justify={"end"}>
                        <Dialog.Close>
                          <Button variant="soft">{t("common.close")}</Button>
                        </Dialog.Close>
                      </Flex>
                    </Dialog.Content>
                  </Dialog.Root>
                </TableCell>
                <TableCell>{log.ip}</TableCell>
                <TableCell>{log.msg_type}</TableCell>
                <TableCell>
                  {log.message.length > 75
                    ? `${log.message.slice(0, 75)}...`
                    : log.message}
                </TableCell>
                <TableCell>{new Date(log.time).toLocaleString()}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
      {/* 分页数字按钮 */}
      <div className="flex justify-center items-center space-x-2 mt-4 gap-2">
        <Button
          disabled={page === 1}
          onClick={() => setPage((p) => Math.max(1, p - 1))}
          title={t("common.previous_page", "Previous page")}
          aria-label={t("common.previous_page", "Previous page")}
        >
          {"<"}
        </Button>
        {pageNumbers.map((p, i) =>
          typeof p === "number" ? (
            <Button
              key={i}
              variant={p === page ? "solid" : "soft"}
              onClick={() => setPage(p)}
            >
              {p}
            </Button>
          ) : (
            <span key={i} className="px-2">
              ...
            </span>
          )
        )}
        <Button
          disabled={page === totalPages}
          onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
          title={t("common.next_page", "Next page")}
          aria-label={t("common.next_page", "Next page")}
        >
          {">"}
        </Button>
      </div>
    </div>
  );
};
export default LogPage;
