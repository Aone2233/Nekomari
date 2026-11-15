import { Button, Dialog, Flex, Select, Text } from "@radix-ui/themes";
import React from "react";
import { useTranslation } from "react-i18next";
import { useNodeDetails } from "@/contexts/NodeDetailsContext";
import type { PingTask } from "@/contexts/PingTaskContext";
import { requireApiSuccess } from "@/lib/apiResponse";

type Leg = { role: string; leg: string; status: string; at?: string; valid_until?: string };
type Snapshot = { state: string; observed_at: string; valid_until: string; dropped_updates: number; legs: Leg[] };

function parseSnapshot(value: unknown): Snapshot | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  const candidate = value as Record<string, unknown>;
  const date = (v: unknown) => typeof v === "string" && Number.isFinite(Date.parse(v));
  if (typeof candidate.state !== "string" || !date(candidate.observed_at) || !date(candidate.valid_until) ||
      !Number.isSafeInteger(candidate.dropped_updates) || (candidate.dropped_updates as number) < 0 ||
      !Array.isArray(candidate.legs) || candidate.legs.length < 1 || candidate.legs.length > 3 ||
      !candidate.legs.every((item: unknown) => {
        if (!item || typeof item !== "object" || Array.isArray(item)) return false;
        const leg = item as Record<string, unknown>;
        return typeof leg.role === "string" && typeof leg.leg === "string" && typeof leg.status === "string" &&
          (leg.at === undefined || date(leg.at)) && (leg.valid_until === undefined || date(leg.valid_until));
      })) return null;
  return value as Snapshot;
}

// Manual, per-node snapshots avoid a request per task/node on every page load.
// A session handshake by itself is never presented as probe success.
export function PingFamilyDiagnostic({ task }: { task: PingTask }) {
  const { t } = useTranslation();
  const { nodeDetail } = useNodeDetails();
  const [open, setOpen] = React.useState(false);
  const [selected, setSelected] = React.useState(task.clients?.[0] ?? "");
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState("");
  const [result, setResult] = React.useState<Snapshot | null>(null);
  const [expired, setExpired] = React.useState(false);
  const request = React.useRef<AbortController | null>(null);
  const expiryTimer = React.useRef<ReturnType<typeof setTimeout> | null>(null);

  const clear = () => {
    request.current?.abort();
    request.current = null;
    if (expiryTimer.current) clearTimeout(expiryTimer.current);
    expiryTimer.current = null;
    setLoading(false);
    setResult(null);
    setError("");
    setExpired(false);
  };
  React.useEffect(() => () => {
    request.current?.abort();
    if (expiryTimer.current) clearTimeout(expiryTimer.current);
  }, []);

  const run = async () => {
    if (!task.id || !selected || request.current || !task.clients?.includes(selected)) return;
    clear();
    const controller = new AbortController();
    request.current = controller;
    setLoading(true);
    const started = performance.now();
    try {
      const response = await fetch("/api/admin/ping/diagnostic", {
        method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ task_id: task.id, client: selected }), signal: controller.signal,
      });
      const envelope = await requireApiSuccess(response, t("ping.diagnostic_error"));
      const snapshot = parseSnapshot(envelope.data);
      if (!snapshot) throw new Error(t("ping.diagnostic_error"));
      if (request.current !== controller) return;
      // Compare the server's own timestamps rather than browser wall clocks;
      // subtract the whole request duration, conservatively counting transit.
      const remaining = Math.min(120_000, Date.parse(snapshot.valid_until) - Date.parse(snapshot.observed_at)) -
        (performance.now() - started);
      if (remaining <= 0) {
        setExpired(true);
      } else {
        setResult(snapshot);
        expiryTimer.current = setTimeout(() => { setResult(null); setExpired(true); }, remaining);
      }
    } catch (cause) {
      if (request.current === controller && !controller.signal.aborted) {
        setError(cause instanceof Error ? cause.message : t("ping.diagnostic_error"));
      }
    } finally {
      if (request.current === controller) {
        request.current = null;
        setLoading(false);
      }
    }
  };

  const knownStates = new Set(["unknown", "offline", "post_only", "session_active", "unavailable"]);
  const knownStatuses = new Set(["unknown", "recorded", "write_unavailable", "permit_unavailable", "dns_error", "no_family_address",
    "family_mismatch", "family_drift", "local_socket", "invalid_target", "canceled", "not_probed", "invalid_outcome"]);
  const stateText = (state: string) => t(`ping.diagnostic_state_${knownStates.has(state) ? state : "unknown"}`);
  const statusText = (status: string) => t(`ping.diagnostic_status_${knownStatuses.has(status) ? status : "unknown"}`);

  return (
    <Dialog.Root open={open} onOpenChange={(next) => { if (!next) clear(); setOpen(next); }}>
      <Dialog.Trigger><Button size="1" variant="soft">{t("ping.diagnostic_button")}</Button></Dialog.Trigger>
      <Dialog.Content maxWidth="520px">
        <Dialog.Title>{t("ping.diagnostic_title")}</Dialog.Title>
        <Flex direction="column" gap="3">
          <Text size="2">{t("ping.diagnostic_hint")}</Text>
          <Select.Root value={selected} onValueChange={(value) => { clear(); setSelected(value); }}>
            <Select.Trigger aria-label={t("common.server")} />
            <Select.Content>
              {(task.clients ?? []).map((uuid) => (
                <Select.Item key={uuid} value={uuid}>{nodeDetail.find((node) => node.uuid === uuid)?.name || uuid}</Select.Item>
              ))}
            </Select.Content>
          </Select.Root>
          <Button type="button" onClick={run} disabled={loading || !task.clients?.includes(selected)}>
            {loading ? t("ping.diagnostic_loading") : t("ping.diagnostic_refresh")}
          </Button>
          {error && <Text role="alert" color="red" size="2">{error}</Text>}
          {expired && <Text role="status" color="amber" size="2">{t("ping.diagnostic_expired")}</Text>}
          {result && (
            <Flex direction="column" gap="2" role="status">
              <Text size="2">{t("ping.diagnostic_connection")}: {stateText(result.state)}</Text>
              {result.legs.map((leg) => (
                <Text size="2" key={`${leg.role}:${leg.leg}`}>
                  {leg.role === "reference" ? t("ping.diagnostic_reference") : t("ping.diagnostic_primary")}
                  {leg.role === "primary" ? ` (${leg.leg.toUpperCase()})` : ""}: {statusText(leg.status)}
                </Text>
              ))}
              <Text size="1" color="gray">{t("ping.diagnostic_observed")}: {result.observed_at}</Text>
              {result.dropped_updates > 0 && <Text size="2" color="amber">{t("ping.diagnostic_dropped", { count: result.dropped_updates })}</Text>}
            </Flex>
          )}
          <Flex justify="end"><Dialog.Close><Button type="button" variant="soft">{t("common.close")}</Button></Dialog.Close></Flex>
        </Flex>
      </Dialog.Content>
    </Dialog.Root>
  );
}
