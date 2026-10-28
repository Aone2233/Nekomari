import { Button, Flex, Select, Text } from "@radix-ui/themes";
import { RefreshCw } from "lucide-react";
import React from "react";
import { useTranslation } from "react-i18next";
import { CartesianGrid, Line, LineChart, XAxis, YAxis, type TooltipPayload } from "recharts";
import { z } from "zod";
import { SettingCard } from "@/components/admin/SettingCard";
import { ChartContainer, ChartTooltip, ChartTooltipContent } from "@/components/ui/chart";
import { formatBytes } from "@/utils/unitHelper";

const METRICS = {
  rss_bytes: "panel.self.rss_bytes",
  heap_bytes: "panel.self.heap_alloc_bytes",
  goroutines: "panel.self.goroutines",
} as const;
type HistoryMetric = keyof typeof METRICS;

const historySchema = z.object({
  status: z.literal("success"),
  data: z.object({
    interval_seconds: z.number().int().positive(),
    retention_days: z.record(z.string(), z.number().int().nonnegative()),
    points: z.array(z.object({
      time: z.string(),
      rss_bytes: z.number().finite().nonnegative().nullable(),
      heap_bytes: z.number().finite().nonnegative().nullable(),
      goroutines: z.number().finite().nonnegative().nullable(),
    })).max(500),
  }),
});
type History = z.infer<typeof historySchema>["data"];
type Loaded = { key: string; history: History | null; error: boolean };

export default function PanelHistory() {
  const { t } = useTranslation();
  const [hours, setHours] = React.useState("24");
  const [metric, setMetric] = React.useState<HistoryMetric>("rss_bytes");
  const [revision, setRevision] = React.useState(0);
  const [loaded, setLoaded] = React.useState<Loaded | null>(null);
  const requestKey = `${hours}:${revision}`;

  React.useEffect(() => {
    const controller = new AbortController();
    void (async () => {
      try {
        const response = await fetch(`/api/admin/pprof/history?hours=${hours}`, {
          cache: "no-store", credentials: "same-origin", signal: controller.signal,
        });
        if (!response.ok) throw new Error("history unavailable");
        const parsed = historySchema.safeParse(await response.json());
        if (!parsed.success) throw new Error("invalid history response");
        if (!controller.signal.aborted) setLoaded({ key: requestKey, history: parsed.data.data, error: false });
      } catch {
        if (!controller.signal.aborted) setLoaded({ key: requestKey, history: null, error: true });
      }
    })();
    return () => controller.abort();
  }, [hours, requestKey]);

  const current = loaded?.key === requestKey ? loaded : null;
  const rows = (current?.history?.points ?? []).flatMap((point) => {
    const time = Date.parse(point.time);
    return Number.isFinite(time) ? [{ time, value: point[metric] }] : [];
  });
  const measured = rows.filter((row) => row.value !== null);
  const latest = measured[measured.length - 1];
  const retention = current?.history?.retention_days[METRICS[metric]];
  const formatValue = (value: number) => metric === "goroutines" ? Math.round(value).toLocaleString() : formatBytes(value);
  const label = metric === "rss_bytes" ? "RSS" : t(metric === "heap_bytes" ? "pprof.heap_alloc" : "pprof.goroutines");

  return (
    <SettingCard title={t("pprof.history_title")} description={t("pprof.history_description")}>
      <Flex direction="column" gap="3" className="w-full py-3" data-testid="panel-history">
        <Flex gap="3" wrap="wrap" align="end" justify="between">
          <Flex gap="3" wrap="wrap" align="end">
            <label className="flex flex-col gap-1">
              <Text size="2">{t("pprof.history_metric")}</Text>
              <Select.Root value={metric} onValueChange={(value) => setMetric(value as HistoryMetric)}>
                <Select.Trigger aria-label={t("pprof.history_metric")} />
                <Select.Content>
                  <Select.Item value="rss_bytes">RSS</Select.Item>
                  <Select.Item value="heap_bytes">{t("pprof.heap_alloc")}</Select.Item>
                  <Select.Item value="goroutines">{t("pprof.goroutines")}</Select.Item>
                </Select.Content>
              </Select.Root>
            </label>
            <label className="flex flex-col gap-1">
              <Text size="2">{t("uptimeHistory.range")}</Text>
              <Select.Root value={hours} onValueChange={setHours}>
                <Select.Trigger aria-label={t("uptimeHistory.range")} />
                <Select.Content>{[1, 24].map((value) => <Select.Item key={value} value={String(value)}>{t("chart.hours", { count: value })}</Select.Item>)}</Select.Content>
              </Select.Root>
            </label>
          </Flex>
          <Button variant="soft" onClick={() => setRevision((value) => value + 1)}>
            <RefreshCw size={16} />{t("common.refresh")}
          </Button>
        </Flex>
        <div>
          <Text as="div" size="1" color="gray">{t("pprof.history_last_bucket")}</Text>
          <Text as="div" size="5" weight="bold" data-testid="panel-history-latest">{latest?.value !== null && latest?.value !== undefined ? formatValue(latest.value) : "-"}</Text>
          {latest && <Text as="div" size="1" color="gray">{new Date(latest.time).toLocaleString()}</Text>}
        </div>
        {!current ? <Text color="gray">{t("pprof.loading")}</Text> : current.error ? (
          <Text color="red" role="alert">{t("pprof.history_error")}</Text>
        ) : retention === 0 ? (
          <Text color="gray" data-testid="panel-history-empty">{t("pprof.history_disabled")}</Text>
        ) : measured.length === 0 ? (
          <Text color="gray" data-testid="panel-history-empty">{t("pprof.history_empty")}</Text>
        ) : (
          <ChartContainer config={{ value: { label, color: "var(--accent-9)" } }} className="h-[240px] w-full" style={{ aspectRatio: "auto" }}>
            <LineChart data={rows} accessibilityLayer margin={{ top: 16, right: 16, bottom: 4, left: 8 }}>
              <CartesianGrid vertical={false} />
              <XAxis dataKey="time" type="number" domain={["dataMin", "dataMax"]} tickFormatter={(value: number) => new Date(value).toLocaleString([], { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" })} />
              <YAxis width={1} mirror tick={{ dx: 8 }} tickFormatter={(value: number) => formatValue(value)} />
              <ChartTooltip cursor={false} content={<ChartTooltipContent labelFormatter={(_value: unknown, payload: TooltipPayload) => {
                const time = payload?.[0]?.payload?.time;
                return typeof time === "number" ? new Date(time).toLocaleString() : "";
              }} formatter={(value: unknown) => typeof value === "number" ? formatValue(value) : "-"} />} />
              <Line dataKey="value" stroke="var(--accent-9)" strokeWidth={2} dot={measured.length <= 30} connectNulls={false} isAnimationActive={false} />
            </LineChart>
          </ChartContainer>
        )}
        <Text as="p" size="2" color="gray">{t("pprof.history_note")}</Text>
        {retention !== undefined && <Text as="p" size="1" color="gray">{t("pprof.history_retention", { days: retention })}</Text>}
      </Flex>
    </SettingCard>
  );
}
