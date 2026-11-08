import React from "react";
import { Badge, Card, Flex, Table, Text } from "@radix-ui/themes";
import { useTranslation } from "react-i18next";
import { useRPC2Call } from "@/contexts/RPC2Context";

type QualityNode = {
  uuid: string;
  name: string;
  last_report_age_seconds?: number | null;
  sample_interval_seconds?: number;
  quality?: Record<string, string>;
  cpu_interval_seconds?: number;
  cgroup_present: boolean;
  disk_mounts: number;
  tcp_counters_present: boolean;
  boot_id_hash_present: boolean;
};

type Response = { now: string; nodes: QualityNode[] };

function ageLabel(seconds?: number | null): string {
  if (typeof seconds !== "number" || !Number.isFinite(seconds)) return "-";
  if (seconds < 60) return `${Math.round(seconds)}s`;
  if (seconds < 3600) return `${Math.round(seconds / 60)}m`;
  return `${(seconds / 3600).toFixed(1)}h`;
}

function unknownKeys(node: QualityNode): string {
  const keys = Object.entries(node.quality ?? {})
    .filter(([, value]) => value !== "ok")
    .map(([key]) => key);
  return keys.length ? keys.join(", ") : "-";
}

export default function ProbeQualityPage() {
  const { t } = useTranslation();
  const { call } = useRPC2Call();
  const [data, setData] = React.useState<Response | null>(null);
  const [error, setError] = React.useState<string | null>(null);

  React.useEffect(() => {
    let active = true;
    call<unknown, Response>("admin:getProbeQuality")
      .then((response) => {
        if (active) {
          setData(response);
          setError(null);
        }
      })
      .catch((err: unknown) => {
        if (active) setError(err instanceof Error ? err.message : String(err));
      });
    return () => {
      active = false;
    };
  }, [call]);

  return (
    <Flex direction="column" gap="4" className="km-admin-probe-quality w-full max-w-[1200px] p-4">
      <div>
        <Text as="div" size="6" weight="bold">
          {t("probeQuality.title", "Probe data quality")}
        </Text>
        <Text as="div" size="2" color="gray">
          {t(
            "probeQuality.description",
            "Last report age, unknown fields, sample interval and v1.8 host detail presence. Unknown is never shown as zero.",
          )}
        </Text>
      </div>
      {error ? (
        <Card>
          <Text color="red">{error}</Text>
        </Card>
      ) : null}
      <Card>
        <Table.Root>
          <Table.Header>
            <Table.Row>
              <Table.ColumnHeaderCell>{t("common.server")}</Table.ColumnHeaderCell>
              <Table.ColumnHeaderCell>{t("probeQuality.age", "Report age")}</Table.ColumnHeaderCell>
              <Table.ColumnHeaderCell>{t("probeQuality.interval", "Interval")}</Table.ColumnHeaderCell>
              <Table.ColumnHeaderCell>{t("probeQuality.unknown", "Unknown fields")}</Table.ColumnHeaderCell>
              <Table.ColumnHeaderCell>CPU</Table.ColumnHeaderCell>
              <Table.ColumnHeaderCell>cgroup</Table.ColumnHeaderCell>
              <Table.ColumnHeaderCell>{t("probeQuality.mounts", "Mounts")}</Table.ColumnHeaderCell>
              <Table.ColumnHeaderCell>TCP</Table.ColumnHeaderCell>
              <Table.ColumnHeaderCell>boot_id</Table.ColumnHeaderCell>
            </Table.Row>
          </Table.Header>
          <Table.Body>
            {(data?.nodes ?? []).map((node) => (
              <Table.Row key={node.uuid}>
                <Table.Cell>
                  <Text weight="medium">{node.name || node.uuid}</Text>
                </Table.Cell>
                <Table.Cell>{ageLabel(node.last_report_age_seconds)}</Table.Cell>
                <Table.Cell>
                  {typeof node.sample_interval_seconds === "number"
                    ? `${node.sample_interval_seconds}s`
                    : "-"}
                </Table.Cell>
                <Table.Cell>
                  <Text size="1" color="gray">
                    {unknownKeys(node)}
                  </Text>
                </Table.Cell>
                <Table.Cell>
                  {typeof node.cpu_interval_seconds === "number" && node.cpu_interval_seconds > 0
                    ? `${node.cpu_interval_seconds.toFixed(1)}s`
                    : "-"}
                </Table.Cell>
                <Table.Cell>
                  <Badge color={node.cgroup_present ? "green" : "gray"}>
                    {node.cgroup_present ? "yes" : "unknown"}
                  </Badge>
                </Table.Cell>
                <Table.Cell>{node.disk_mounts > 0 ? String(node.disk_mounts) : "unknown"}</Table.Cell>
                <Table.Cell>
                  <Badge color={node.tcp_counters_present ? "green" : "gray"}>
                    {node.tcp_counters_present ? "yes" : "unknown"}
                  </Badge>
                </Table.Cell>
                <Table.Cell>
                  <Badge color={node.boot_id_hash_present ? "green" : "gray"}>
                    {node.boot_id_hash_present ? "yes" : "unknown"}
                  </Badge>
                </Table.Cell>
              </Table.Row>
            ))}
          </Table.Body>
        </Table.Root>
      </Card>
    </Flex>
  );
}
