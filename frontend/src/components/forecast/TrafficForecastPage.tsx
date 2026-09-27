import { Badge, Button, Card, Flex, Heading, Table, Text, TextField } from "@radix-ui/themes";
import { useTranslation } from "react-i18next";
import React, { useCallback, useMemo, useState } from "react";
import { formatBytes } from "./forecastFormat";

/**
 * Traffic forecast (roadmap H5).
 *
 * Takes the rows and the two writes as props, so the mounted fixture can put a node projected to exceed
 * its limit, a node with too little data and a change of cycle day on screen without a server.
 *
 * The design follows one rule: **a projection is shown with its basis or not at all.** The server refuses
 * to project from too little data and says why, and the page reports that reason rather than a blank cell
 * — because "not enough data" and "no problem" look identical in an empty cell, and the difference is
 * exactly what the operator is there to find out. For the same reason a projected number is accompanied
 * by the samples behind it and the uncertainty: a figure that cannot be argued with is not a figure anyone
 * should act on.
 *
 * The cycle day is editable on the page rather than only in settings, because it is the one input that
 * changes every number here, and an operator checking whether it matches their agent should be able to see
 * the effect immediately.
 */

/** ForecastBasis mirrors the server's Basis. */
export interface ForecastBasis {
  samples: number;
  observed_for: number;
  coverage: number;
  bytes_per_day: number;
  uncertainty: number;
  cycle_days: number;
}

/** ForecastRow mirrors one row of the server's response. */
export interface ForecastRow {
  uuid: string;
  name: string;
  /** "insufficient" means no projection was made, and reason says why. */
  method: string;
  reason?: string;
  used_bytes: number;
  projected_bytes: number;
  limit_bytes: number;
  limit_type: string;
  limit_set: boolean;
  projected_fraction: number;
  would_exceed: boolean;
  crosses_at?: string;
  crosses_in_days?: number;
  crosses_in_cycle: boolean;
  basis: ForecastBasis;
  warning?: { threshold: number; projected_fraction: number; message: string };
}

export interface ForecastCycle {
  start: string;
  end: string;
  reset_day: number;
  location: string;
}

export interface TrafficForecastPageProps {
  rows: ForecastRow[];
  cycle: ForecastCycle;
  threshold: number;
  onSetCycleDay?: (day: number) => Promise<void>;
  /** onPreview asks the server to project against a day without storing it. */
  onPreview?: (day: number) => Promise<void>;
  loading?: boolean;
}

export const TrafficForecastPage: React.FC<TrafficForecastPageProps> = ({
  rows,
  cycle,
  threshold,
  onSetCycleDay,
  onPreview,
  loading = false,
}) => {
  const { t } = useTranslation();
  const [day, setDay] = useState(String(cycle.reset_day));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const exceeding = useMemo(() => rows.filter((row) => row.would_exceed).length, [rows]);
  const projected = useMemo(
    () => rows.filter((row) => row.method !== "insufficient").length,
    [rows],
  );
  const refused = useMemo(
    () => rows.filter((row) => row.method === "insufficient").length,
    [rows],
  );

  const run = useCallback(
    async (action?: (day: number) => Promise<void>) => {
      if (!action) return;
      const parsed = Number(day);
      if (!Number.isInteger(parsed) || parsed < 1 || parsed > 31) {
        setError("1–31");
        return;
      }
      setBusy(true);
      setError(null);
      try {
        await action(parsed);
      } catch (cause) {
        setError(cause instanceof Error ? cause.message : String(cause));
      } finally {
        setBusy(false);
      }
    },
    [day],
  );

  return (
    <Flex direction="column" gap="4" p="4" data-testid="forecast-page">
      <Flex direction="column" gap="1">
        <Heading size="6">{t("forecast.title")}</Heading>
        <Text size="2" color="gray">
          {t("forecast.subtitle")}
        </Text>
      </Flex>

      <Card data-testid="forecast-cycle">
        <Flex align="center" gap="4" wrap="wrap">
          <Flex direction="column" gap="1">
            <Text size="1" color="gray">
              {t("forecast.cycleWindow")}
            </Text>
            <Text size="2" data-testid="forecast-window">
              {cycle.start} → {cycle.end}
            </Text>
          </Flex>
          <Flex align="center" gap="2">
            <Text size="2">{t("forecast.cycleDay")}</Text>
            <TextField.Root
              type="number"
              value={day}
              onChange={(event) => setDay(event.target.value)}
              style={{ width: 72 }}
              data-testid="forecast-cycle-day"
            />
            <Button
              size="2"
              variant="soft"
              onClick={() => void run(onPreview)}
              disabled={!onPreview || busy}
              data-testid="forecast-preview"
            >
              {t("forecast.preview")}
            </Button>
            <Button
              size="2"
              onClick={() => void run(onSetCycleDay)}
              disabled={!onSetCycleDay || busy}
              data-testid="forecast-save-day"
            >
              {t("forecast.saveDay")}
            </Button>
          </Flex>
          <Flex direction="column" gap="1">
            <Text size="1" color="gray">
              {t("forecast.timezone")}
            </Text>
            {/* Stated, not buried: the panel computes the cycle in its own timezone because the agent does
                not report its own, and a mismatch shifts every projection here. */}
            <Text size="2" data-testid="forecast-location">
              {cycle.location}
            </Text>
          </Flex>
          <Badge data-testid="forecast-threshold" data-threshold={threshold}>
            {t("forecast.threshold").replace("{{percent}}", String(Math.round(threshold * 100)))}
          </Badge>
        </Flex>
        {error && (
          <Text size="1" color="red" mt="2" data-testid="forecast-error">
            {t("forecast.error")}: {error}
          </Text>
        )}
      </Card>

      <Flex gap="3" wrap="wrap">
        <Badge size="2" color={exceeding ? "red" : "gray"}
               data-testid="forecast-exceeding" data-count={exceeding}>
          {t("forecast.exceeding").replace("{{count}}", String(exceeding))}
        </Badge>
        <Badge size="2" data-testid="forecast-projected" data-count={projected}>
          {t("forecast.projected").replace("{{count}}", String(projected))}
        </Badge>
        <Badge size="2" color="amber" data-testid="forecast-refused" data-count={refused}>
          {t("forecast.refused").replace("{{count}}", String(refused))}
        </Badge>
      </Flex>

      <Card data-testid="forecast-table-card">
        {loading && rows.length === 0 ? (
          <Text size="2" color="gray" data-testid="forecast-loading">
            {t("forecast.loading")}
          </Text>
        ) : (
          <Table.Root size="1">
            <Table.Header>
              <Table.Row>
                <Table.ColumnHeaderCell>{t("forecast.node")}</Table.ColumnHeaderCell>
                <Table.ColumnHeaderCell>{t("forecast.used")}</Table.ColumnHeaderCell>
                <Table.ColumnHeaderCell>{t("forecast.projectedEnd")}</Table.ColumnHeaderCell>
                <Table.ColumnHeaderCell>{t("forecast.limit")}</Table.ColumnHeaderCell>
                <Table.ColumnHeaderCell>{t("forecast.crosses")}</Table.ColumnHeaderCell>
                <Table.ColumnHeaderCell>{t("forecast.basis")}</Table.ColumnHeaderCell>
              </Table.Row>
            </Table.Header>
            <Table.Body>
              {rows.map((row) => {
                const insufficient = row.method === "insufficient";
                return (
                  <Table.Row
                    key={row.uuid}
                    data-testid="forecast-row"
                    data-uuid={row.uuid}
                    data-method={row.method}
                    data-exceeds={String(row.would_exceed)}
                  >
                    <Table.Cell>
                      {row.name || row.uuid}
                      {row.limit_type ? (
                        <Text size="1" color="gray">
                          {" "}
                          · {row.limit_type}
                        </Text>
                      ) : null}
                    </Table.Cell>
                    <Table.Cell data-testid="forecast-row-used">{formatBytes(row.used_bytes)}</Table.Cell>
                    <Table.Cell data-testid="forecast-row-projected">
                      {insufficient ? (
                        // The refusal, in the cell the number would have been in. A blank here would read as
                        // "nothing to report", which is the opposite of what it means.
                        <Text size="1" color="amber" data-testid="forecast-row-reason">
                          {row.reason || t("forecast.insufficient")}
                        </Text>
                      ) : (
                        <Flex align="center" gap="2">
                          {formatBytes(row.projected_bytes)}
                          {row.limit_set && <PercentBadge fraction={row.projected_fraction} />}
                        </Flex>
                      )}
                    </Table.Cell>
                    <Table.Cell>
                      {row.limit_set ? formatBytes(row.limit_bytes) : t("forecast.noLimit")}
                    </Table.Cell>
                    <Table.Cell data-testid="forecast-row-crosses">
                      {insufficient
                        ? "—"
                        : row.crosses_in_cycle
                          ? t("forecast.inDays").replace("{{days}}", (row.crosses_in_days ?? 0).toFixed(1))
                          : t("forecast.notThisCycle")}
                    </Table.Cell>
                    <Table.Cell data-testid="forecast-row-basis">
                      {insufficient ? (
                        "—"
                      ) : (
                        <Text size="1" color="gray">
                          {t("forecast.basisDetail")
                            .replace("{{samples}}", String(row.basis.samples))
                            .replace("{{coverage}}", String(Math.round(row.basis.coverage * 100)))
                            .replace("{{uncertainty}}", String(Math.round(row.basis.uncertainty * 100)))}
                        </Text>
                      )}
                    </Table.Cell>
                  </Table.Row>
                );
              })}
            </Table.Body>
          </Table.Root>
        )}
      </Card>
    </Flex>
  );
};

/** PercentBadge colours a projection by whether it reaches the limit. */
const PercentBadge: React.FC<{ fraction: number }> = ({ fraction }) => (
  <Badge
    size="1"
    color={fraction > 1 ? "red" : fraction >= 0.9 ? "amber" : "gray"}
    data-testid="forecast-row-percent"
    data-fraction={fraction.toFixed(3)}
  >
    {Math.round(fraction * 100)}%
  </Badge>
);

export default TrafficForecastPage;
