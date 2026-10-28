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
  main_wait_count: "panel.pool.main.wait_count",
  main_wait_ms: "panel.pool.main.wait_ms",
  write_wait_count: "panel.pool.metrics_write.wait_count",
  write_wait_ms: "panel.pool.metrics_write.wait_ms",
  read_wait_count: "panel.pool.metrics_read.wait_count",
  read_wait_ms: "panel.pool.metrics_read.wait_ms",
} as const;
type HistoryMetric = keyof typeof METRICS;

// Older servers do not include pool readings. Treat absent fields as unknown.
const poolReading = z.number().finite().nonnegative().nullable().optional().transform((value) => value ?? null);

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
      main_wait_count: poolReading,
      main_wait_ms: poolReading,
      write_wait_count: poolReading,
      write_wait_ms: poolReading,
      read_wait_count: poolReading,
      read_wait_ms: poolReading,
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
  const poolMetric = metric.includes("_wait_");
  const formatValue = (value: number) => {
    if (metric.endsWith("_wait_ms")) return `${value.toLocaleString(undefined, { maximumFractionDigits: 3 })} ms`;
    if (metric === "goroutines" || metric.endsWith("_wait_count")) return Math.round(value).toLocaleString();
    return formatBytes(value);
  };
  const labels: Record<HistoryMetric, string> = {
    rss_bytes: "RSS",
    heap_bytes: t("pprof.heap_alloc"),
    goroutines: t("pprof.goroutines"),
    main_wait_count: t("pprof.pool_main_wait_count"),
    main_wait_ms: t("pprof.pool_main_wait_ms"),
    write_wait_count: t("pprof.pool_write_wait_count"),
    write_wait_ms: t("pprof.pool_write_wait_ms"),
    read_wait_count: t("pprof.pool_read_wait_count"),
    read_wait_ms: t("pprof.pool_read_wait_ms"),
  };
  const label = labels[metric];

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
                  {(Object.keys(METRICS) as HistoryMetric[]).map((name) => <Select.Item key={name} value={name}>{labels[name]}</Select.Item>)}
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
        {poolMetric && <Text as="p" size="2" color="gray" data-testid="panel-pool-note">{t("pprof.pool_history_note")}</Text>}
        {retention !== undefined && <Text as="p" size="1" color="gray">{t("pprof.history_retention", { days: retention })}</Text>}
      </Flex>
    </SettingCard>
  );
}
