import type { TFunction } from "i18next";
import type { PingMetricStat } from "@/types/metrics";

type PingMetricStatContentProps = {
  stat: PingMetricStat;
  t: TFunction;
};

/**
 * formatMilliseconds renders one latency figure.
 *
 * 亚毫秒的测量报 "<1 ms" 而不是 "0 ms"：0 读起来像"没有延迟"，而它其实表示"低于 1 ms 这一档"——
 * 后端改成小数毫秒之后，这两种情况必须能分开。其余保留 1 位小数，因为 10.4 与 10.9 的差别在
 * ping 图上是可见的，取整会把它抹平。
 *
 * null/undefined（服务端没有这一项）返回 null 而不是占位符：那一行干脆不渲染，表格里不会出现
 * 一个看起来像"测量到了 0"的破折号。
 */
const formatMilliseconds = (value: number | null | undefined): string | null => {
  if (typeof value !== "number" || !Number.isFinite(value)) return null;
  if (value < 1) return "<1 ms";
  return `${value.toFixed(1)} ms`;
};

const PingMetricStatContent = ({ stat, t }: PingMetricStatContentProps) => {
  const rows: Array<[string, string]> = [];
  const push = (label: string, value: string | null) => {
    if (value !== null) rows.push([label, value]);
  };

  push(
    t("chart.lossRate"),
    typeof stat.loss === "number"
      ? `${stat.loss.toFixed(1)}%${stat.loss_approximate ? ` ${t("chart.approximate")}` : ""}`
      : "-",
  );
  push(t("chart.min"), formatMilliseconds(stat.min));
  push(t("chart.max"), formatMilliseconds(stat.max));
  push(t("chart.avg"), formatMilliseconds(stat.avg));
  push(t("chart.latest"), formatMilliseconds(stat.latest));
  push("P50", formatMilliseconds(stat.p50));
  push("P99", formatMilliseconds(stat.p99));
  push(t("chart.sampling.stddev"), formatMilliseconds(stat.stddev));
  if (typeof stat.p99_p50_ratio === "number" && (stat.valid ?? 0) > 0) {
    rows.push([t("chart.volatility"), stat.p99_p50_ratio.toFixed(2)]);
  }
  rows.push([t("chart.total"), String(stat.total)]);
  rows.push([t("chart.valid"), stat.valid_known === false ? "-" : String(stat.valid)]);
  if (stat.interval) rows.push([t("chart.interval"), `${stat.interval}s`]);
  if (stat.type) rows.push([t("common.type"), stat.type.toUpperCase()]);

  return (
    <div className="km-ping-metric-stat grid grid-cols-[max-content_1fr] gap-x-3 gap-y-1">
      {rows.map(([label, value]) => (
        <div key={label} className="contents">
          <span className="text-muted-foreground">{label}</span>
          <span className="text-right font-mono tabular-nums">{value}</span>
        </div>
      ))}
    </div>
  );
};

export default PingMetricStatContent;
