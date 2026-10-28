import React from "react";
import { Button, Card, Flex, Select, Text } from "@radix-ui/themes";
import { RefreshCw } from "lucide-react";
import { useTranslation } from "react-i18next";
import { CartesianGrid, Line, LineChart, XAxis, YAxis, type TooltipPayload } from "recharts";
import { ChartContainer, ChartTooltip, ChartTooltipContent } from "@/components/ui/chart";
import { useNodeList } from "@/contexts/NodeListContext";
import { useRPC2Call } from "@/contexts/RPC2Context";
import type { MetricSeries, QueryMetricsResponse } from "@/types/metrics";
import { formatUptime } from "@/utils/timeFormat";

const METRIC = "system.uptime_seconds";
const RANGES = [1, 24, 168] as const;

type Loaded = { key: string; series: MetricSeries | null; error: string | null };

export default function UptimeHistoryPage() {
  const { t } = useTranslation();
  const { nodeList, isLoading: nodesLoading, error: nodesError, refresh: refreshNodes } = useNodeList();
  const { call } = useRPC2Call();
  const [selected, setSelected] = React.useState("");
  const [hours, setHours] = React.useState<number>(24);
  const [revision, setRevision] = React.useState(0);
  const [loaded, setLoaded] = React.useState<Loaded | null>(null);
  const uuid = nodeList?.some((node) => node.uuid === selected) ? selected : nodeList?.[0]?.uuid ?? "";
  const requestKey = `${uuid}:${hours}:${revision}`;

  React.useEffect(() => {
    if (!uuid) return;
    let active = true;
    call<unknown, QueryMetricsResponse>("public:queryMetrics", {
      metric_key: METRIC, entity_id: uuid, hours, max_points: 500, aggregation: "last", fill_empty: true,
    }, { timeout: 30000 }).then((response) => {
      if (active) setLoaded({ key: requestKey, series: response?.series?.find((item) => item.metric_key === METRIC && item.entity_id === uuid) ?? null, error: null });
    }).catch((error: unknown) => {
      if (active) setLoaded({ key: requestKey, series: null, error: error instanceof Error ? error.message : String(error) });
    });
    return () => { active = false; };
  }, [call, hours, requestKey, uuid]);

  const current = loaded?.key === requestKey ? loaded : null;
  const rows = (current?.series?.points ?? []).flatMap((point) => {
    const time = Date.parse(point.time);
    if (!Number.isFinite(time)) return [];
    const value = typeof point.value === "number" && Number.isFinite(point.value) && point.value >= 0 ? point.value : null;
    return [{ time, uptime: value }];
  });
  const measured = rows.filter((row) => row.uptime !== null);
  const latest = measured[measured.length - 1];
  const selectedNode = nodeList?.find((node) => node.uuid === uuid);

  return (
    <Flex direction="column" gap="4" className="km-admin-uptime-history w-full max-w-[1100px] p-4">
      <div>
        <Text as="div" size="6" weight="bold">{t("uptimeHistory.title")}</Text>
        <Text as="div" size="2" color="gray">{t("uptimeHistory.description")}</Text>
      </div>
      <Card>
        <Flex gap="3" wrap="wrap" align="end" justify="between">
          <Flex gap="3" wrap="wrap" align="end">
            <label className="flex flex-col gap-1">
              <Text size="2">{t("uptimeHistory.node")}</Text>
              <Select.Root value={uuid} onValueChange={setSelected} disabled={!uuid}>
                <Select.Trigger aria-label={t("uptimeHistory.node")} />
                <Select.Content>{(nodeList ?? []).map((node) => <Select.Item key={node.uuid} value={node.uuid}>{node.name || node.uuid}</Select.Item>)}</Select.Content>
              </Select.Root>
            </label>
            <label className="flex flex-col gap-1">
              <Text size="2">{t("uptimeHistory.range")}</Text>
              <Select.Root value={String(hours)} onValueChange={(value) => setHours(Number(value))}>
                <Select.Trigger aria-label={t("uptimeHistory.range")} />
                <Select.Content>{RANGES.map((range) => <Select.Item key={range} value={String(range)}>{range === 168 ? t("chart.days", { count: 7 }) : t("chart.hours", { count: range })}</Select.Item>)}</Select.Content>
              </Select.Root>
            </label>
          </Flex>
          <Button variant="soft" onClick={() => { refreshNodes(); setRevision((value) => value + 1); }}>
            <RefreshCw size={16} />{t("uptimeHistory.refresh")}
          </Button>
        </Flex>
      </Card>
      <Card>
        <Flex direction="column" gap="3">
          <div>
            <Text as="div" size="2" color="gray">{selectedNode?.name || t("uptimeHistory.node")}</Text>
            <Text as="div" size="5" weight="bold" data-testid="uptime-latest">{latest ? formatUptime(latest.uptime, t) : "-"}</Text>
            <Text as="div" size="1" color="gray">{latest ? t("uptimeHistory.latestAt", { time: new Date(latest.time).toLocaleString() }) : current?.error ? t("uptimeHistory.error") : t("uptimeHistory.noData")}</Text>
          </div>
          {nodesError && <Text color="red" size="2" role="alert">{nodesError}</Text>}
          {current?.error && <Text color="red" size="2" role="alert">{t("uptimeHistory.error")}: {current.error}</Text>}
          {(nodesLoading || (uuid && !current)) ? <Text color="gray">{t("uptimeHistory.loading")}</Text> : current?.error ? null : measured.length === 0 ? (
            <Flex align="center" justify="center" className="h-[220px]"><Text color="gray">{t("uptimeHistory.noData")}</Text></Flex>
          ) : (
            <ChartContainer config={{ uptime: { label: t("nodeCard.uptime"), color: "var(--accent-9)" } }} className="h-[260px] w-full" style={{ aspectRatio: "auto" }}>
              <LineChart data={rows} accessibilityLayer margin={{ top: 16, right: 16, bottom: 4, left: 8 }}>
                <CartesianGrid vertical={false} />
                <XAxis dataKey="time" type="number" domain={["dataMin", "dataMax"]} tickFormatter={(value: number) => new Date(value).toLocaleString([], { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" })} />
                <YAxis width={1} mirror tick={{ dx: 8 }} tickFormatter={(value: number) => formatUptime(value, t)} />
                <ChartTooltip cursor={false} content={<ChartTooltipContent labelFormatter={(_value: unknown, payload: TooltipPayload) => {
                  const time = payload?.[0]?.payload?.time;
                  return typeof time === "number" ? new Date(time).toLocaleString() : "";
                }} formatter={(value: unknown) => formatUptime(typeof value === "number" ? value : null, t)} />} />
                <Line dataKey="uptime" type="stepAfter" stroke="var(--accent-9)" strokeWidth={2} dot={measured.length <= 30} connectNulls={false} isAnimationActive={false} />
              </LineChart>
            </ChartContainer>
          )}
          <Text as="p" size="2" color="gray" data-testid="uptime-history-note">{t("chart.uptimeHistoryNote")}</Text>
          <Text as="p" size="1" color="gray">{t("uptimeHistory.retention", { days: current?.series?.retention_days ?? "-" })}</Text>
        </Flex>
      </Card>
    </Flex>
  );
}
