import React from "react";
import { Button, Dialog, Flex, Text } from "@radix-ui/themes";
import { useTranslation } from "react-i18next";
import { requireApiSuccess } from "@/lib/apiResponse";

type Result = { items: { client: string; reason: string; time: string; count: number; role?: string; protocol?: string }[]; limited: boolean };

export function PingUnknownDiagnostic({ taskID }: { taskID: number }) {
  const { t } = useTranslation();
  const [open, setOpen] = React.useState(false);
  const [data, setData] = React.useState<Result | null>(null);
  const [error, setError] = React.useState("");
  const [loading, setLoading] = React.useState(false);
  const load = async () => {
    setLoading(true); setError("");
    try {
      const response = await fetch("/api/admin/ping/unknowns", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ task_id: taskID }) });
      const envelope = await requireApiSuccess(response, t("common.error"));
      const value: unknown = envelope.data;
      if (!value || typeof value !== "object" || !Array.isArray((value as Result).items)) throw new Error(t("common.error"));
      setData(value as Result);
    }
    catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); }
    finally { setLoading(false); }
  };
  return <Dialog.Root open={open} onOpenChange={(value) => { setOpen(value); if (value) void load(); }}>
    <Dialog.Trigger><Button size="1" variant="soft">{t("ping.unknownDiagnostics", "Recent probe errors")}</Button></Dialog.Trigger>
    <Dialog.Content maxWidth="600px">
      <Dialog.Title>{t("ping.unknownDiagnostics", "Recent probe errors")}</Dialog.Title>
      <Dialog.Description size="2">{t("ping.unknownDiagnosticsHint", "Cycles that could not start in the past hour. Older agents may not report these errors.")}</Dialog.Description>
      <Flex direction="column" gap="2" mt="3">
        {loading && <Text>{t("common.loading", "Loading...")}</Text>}
        {error && <Text color="red" role="alert">{error}</Text>}
        {!loading && data && data.items.length === 0 && <Text>{t("ping.unknownDiagnosticsEmpty", "No reported probe errors in this period.")}</Text>}
        {data?.items.map((item, i) => <Text key={`${item.client}-${item.reason}-${item.time}-${i}`} size="2">{new Date(item.time).toLocaleString()} · {item.client} · {item.protocol || "—"} · {item.role || "primary"} · {item.reason} ({item.count})</Text>)}
        {data?.limited && <Text size="1">{t("ping.unknownDiagnosticsLimited", "Only the first 100 results are shown.")}</Text>}
        <Flex gap="2" justify="end"><Button variant="soft" onClick={() => void load()} disabled={loading}>{t("common.refresh", "Refresh")}</Button><Dialog.Close><Button>{t("common.close")}</Button></Dialog.Close></Flex>
      </Flex>
    </Dialog.Content>
  </Dialog.Root>;
}
