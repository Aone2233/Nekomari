import { Badge, Card, Flex, Heading, SegmentedControl, Table, Text } from "@radix-ui/themes";
import { useTranslation } from "react-i18next";
import React from "react";
import type {
  SlaIncident,
  SlaNodeReport,
  SlaReport,
  SlaWindow,
} from "@/types/Sla";
import { SLA_WINDOWS } from "@/types/Sla";
import { availabilityText, coverageText, duration, formatTime } from "./slaFormat";

/**
 * The SLA report table (roadmap H1).
 *
 * Takes the report as a prop rather than fetching it, so the mounted fixture can put a
 * loading state, a no-data node, an outage and a hidden node on screen without a server.
 * The page that owns the fetch is `pages/status`.
 *
 * Three presentation rules come from the server's definitions, and each is a place this
 * component could undo the care taken behind it:
 *
 *   - **`has_data === false` is not 0%.** It renders as "no data", because a fraction of
 *     zero with no evidence behind it is the shape a dead node takes.
 *   - **Coverage is shown beside availability, never instead of it.** A node that
 *     reported half a window and was up for all of it is not the same as one that
 *     reported throughout, and one number cannot say both.
 *   - **A reporting gap is not an outage.** It is listed as a gap, in its own column,
 *     because the panel does not know the node was down — only that it stopped
 *     reporting, which is a different claim.
 */

export interface SlaReportTableProps {
  report: SlaReport | null;
  loading?: boolean;
  error?: string | null;
  window: SlaWindow;
  onWindowChange?: (window: SlaWindow) => void;
  /** Display names by entity id, so the table shows nodes rather than uuids. */
  nodeNames?: Record<string, string>;
  /** Task names by id, so an outage says what the task is. */
  taskNames?: Record<string, string>;
}

const IncidentList: React.FC<{ incidents: SlaIncident[] | null; emptyLabel: string }> = ({
  incidents,
  emptyLabel,
}) => {
  if (!incidents || incidents.length === 0) {
    return (
      <Text size="1" color="gray">
        {emptyLabel}
      </Text>
    );
  }
  return (
    <Flex direction="column" gap="1">
      {incidents.map((incident, index) => (
        <Text size="1" key={`${incident.Start}-${index}`}>
          {formatTime(incident.Start)} → {formatTime(incident.End)} · {duration(incident.duration)}
        </Text>
      ))}
    </Flex>
  );
};

const NodeSection: React.FC<{
  node: SlaNodeReport;
  name: string;
  taskNames?: Record<string, string>;
}> = ({ node, name, taskNames }) => {
  const { t } = useTranslation();

  return (
    <Card data-testid="sla-node" data-entity-id={node.entity_id} data-has-report={String(node.has_report)}>
      <Flex direction="column" gap="3">
        <Flex align="center" justify="between" gap="2">
          <Heading size="4">{name}</Heading>
          <Flex gap="2" align="center">
            <Badge data-testid="sla-node-presence" data-coverage={node.presence.coverage.toFixed(4)}>
              {coverageText(
                node.presence.observed_buckets,
                node.presence.expected_buckets,
                node.presence.coverage,
                t,
              )}
            </Badge>
          </Flex>
        </Flex>

        {!node.has_report && (
          <Text size="2" color="gray" data-testid="sla-node-nodata">
            {t("status.nodeNoData")}
          </Text>
        )}

        {node.tasks && node.tasks.length > 0 && (
          <Table.Root size="1" data-testid="sla-task-table">
            <Table.Header>
              <Table.Row>
                <Table.ColumnHeaderCell>{t("status.task")}</Table.ColumnHeaderCell>
                <Table.ColumnHeaderCell>{t("status.availability")}</Table.ColumnHeaderCell>
                <Table.ColumnHeaderCell>{t("status.coverage")}</Table.ColumnHeaderCell>
                <Table.ColumnHeaderCell>{t("status.latency")}</Table.ColumnHeaderCell>
                <Table.ColumnHeaderCell>{t("status.outages")}</Table.ColumnHeaderCell>
              </Table.Row>
            </Table.Header>
            <Table.Body>
              {node.tasks.map((task) => (
                <Table.Row
                  key={task.task_id || "untagged"}
                  data-testid="sla-task-row"
                  data-task-id={task.task_id}
                  data-has-data={String(task.loss.has_data)}
                >
                  <Table.Cell>
                    {taskNames?.[task.task_id] ?? task.task_id ?? t("status.untaggedSeries")}
                  </Table.Cell>
                  <Table.Cell data-testid="sla-task-availability">
                    {availabilityText(task.loss, t)}
                  </Table.Cell>
                  <Table.Cell data-testid="sla-task-coverage">
                    {coverageText(
                      task.loss.presence.observed_buckets,
                      task.loss.presence.expected_buckets,
                      task.loss.presence.coverage,
                      t,
                    )}
                  </Table.Cell>
                  <Table.Cell data-testid="sla-task-latency">
                    {task.latency.has_data
                      ? t("status.latencyValues")
                          .replace("{{p50}}", task.latency.p50_ms.toFixed(1))
                          .replace("{{p95}}", task.latency.p95_ms.toFixed(1))
                          .replace("{{p99}}", task.latency.p99_ms.toFixed(1))
                      : t("status.noData")}
                  </Table.Cell>
                  <Table.Cell data-testid="sla-task-outages">
                    <IncidentList incidents={task.outages} emptyLabel={t("status.noOutages")} />
                  </Table.Cell>
                </Table.Row>
              ))}
            </Table.Body>
          </Table.Root>
        )}

        {node.reporting_gaps && node.reporting_gaps.length > 0 && (
          <Flex direction="column" gap="1" data-testid="sla-reporting-gaps">
            <Text size="2" weight="medium">
              {t("status.reportingGaps")}
            </Text>
            <IncidentList incidents={node.reporting_gaps} emptyLabel={t("status.noOutages")} />
          </Flex>
        )}
      </Flex>
    </Card>
  );
};

export const SlaReportTable: React.FC<SlaReportTableProps> = ({
  report,
  loading = false,
  error = null,
  window: selectedWindow,
  onWindowChange,
  nodeNames,
  taskNames,
}) => {
  const { t } = useTranslation();

  const windowControl = (
    <Flex gap="2" align="center" data-testid="sla-window-control">
      <Text size="2">{t("status.window")}</Text>
      <SegmentedControl.Root
        value={selectedWindow}
        onValueChange={(value) => onWindowChange?.(value as SlaWindow)}
      >
        {SLA_WINDOWS.map((option) => (
          <SegmentedControl.Item key={option} value={option}>
            {t(`status.window_${option}`)}
          </SegmentedControl.Item>
        ))}
      </SegmentedControl.Root>
    </Flex>
  );

  if (loading) {
    return (
      <Flex direction="column" gap="4">
        {windowControl}
        <Text size="2" color="gray" data-testid="sla-loading">
          {t("status.loading")}
        </Text>
      </Flex>
    );
  }

  if (error) {
    return (
      <Flex direction="column" gap="4">
        {windowControl}
        <Text size="2" color="red" data-testid="sla-error">
          {t("status.error")}: {error}
        </Text>
      </Flex>
    );
  }

  if (!report) {
    return (
      <Flex direction="column" gap="4">
        {windowControl}
        <Text size="2" color="gray" data-testid="sla-empty">
          {t("status.empty")}
        </Text>
      </Flex>
    );
  }

  return (
    <Flex direction="column" gap="4" data-testid="sla-report" data-window={report.window}>
      <Flex align="center" justify="between" gap="3" wrap="wrap">
        {windowControl}
        <Text size="1" color="gray" data-testid="sla-interval">
          {t("status.sampleInterval").replace(
            "{{seconds}}",
            String(Math.round(report.interval_seconds)),
          )}
        </Text>
      </Flex>

      {report.clamped && (
        <Text size="1" color="amber" data-testid="sla-clamped">
          {t("status.clamped")}: {report.clamped}
        </Text>
      )}

      {report.nodes.length === 0 ? (
        <Text size="2" color="gray" data-testid="sla-empty">
          {t("status.empty")}
        </Text>
      ) : (
        report.nodes.map((node) => (
          <NodeSection
            key={node.entity_id}
            node={node}
            name={nodeNames?.[node.entity_id] ?? node.entity_id}
            taskNames={taskNames}
          />
        ))
      )}
    </Flex>
  );
};

export default SlaReportTable;
