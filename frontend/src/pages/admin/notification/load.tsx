import Loading from "@/components/loading";
import NodeSelectorDialog from "@/components/NodeSelectorDialog";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useLoadAlert, type LoadAlert } from "@/contexts/LoadAlertContext";
import { LoadAlertProvider } from "@/contexts/LoadAlertProvider";
import { useNodeDetails } from "@/contexts/NodeDetailsContext";
import { NodeDetailsProvider } from "@/contexts/NodeDetailsProvider";
import { usePingTask } from "@/contexts/PingTaskContext";
import { requireApiSuccess } from "@/lib/apiResponse";

import {
  Button,
  Checkbox,
  Dialog,
  Flex,
  IconButton,
  Select,
  TextField,
} from "@radix-ui/themes";
import { FlaskConical, MoreHorizontal, Pencil, Trash } from "lucide-react";
import React from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";

const LoadPage = () => {
  return (
    <LoadAlertProvider>
      <NodeDetailsProvider>
        <InnerLayout />
      </NodeDetailsProvider>
    </LoadAlertProvider>
  );
};

const InnerLayout = () => {
  const { loadAlerts, isLoading, error } = useLoadAlert();
  const { isLoading: nodeDetailLoading, error: nodeDetailError } =
    useNodeDetails();
  const { t } = useTranslation();
  if (isLoading || nodeDetailLoading) {
    return <Loading />;
  }
  if (error || nodeDetailError) {
    return <div>{error || nodeDetailError}</div>;
  }
  return (
    <Flex direction="column" gap="4" className="km-page-admin-notification-load p-4">
      <div className="flex justify-between items-center">
        <label className="text-2xl font-bold">
          {t("notification.load.title")}
        </label>
        <AddButton />
      </div>

      <div className="rounded-xl overflow-hidden">
        <Table>
          <TableHeader>
            <TableHead>{t("common.name")}</TableHead>
            <TableHead>{t("common.server")}</TableHead>
            <TableHead>{t("loadAlert.metric")}</TableHead>
            <TableHead>{t("common.threshold")}</TableHead>
            <TableHead>{t("loadAlert.ratio")}</TableHead>
            <TableHead>{t("ping.interval")}</TableHead>
            <TableHead>{t("common.action")}</TableHead>
          </TableHeader>
          <TableBody>
            {loadAlerts
              ?.slice()
              .sort((a, b) => (b.id ?? 0) - (a.id ?? 0))
              .map((alert) => (
                <Row key={alert.id} alert={alert} />
              ))}
          </TableBody>
        </Table>
      </div>
    </Flex>
  );
};

const Row = ({ alert }: { alert: LoadAlert }) => {
  const { t } = useTranslation();
  const { refresh } = useLoadAlert();
  const { nodeDetail } = useNodeDetails();
  const [editOpen, setEditOpen] = React.useState(false);
  const [editSaving, setEditSaving] = React.useState(false);
  const saveInFlight = React.useRef(false);
  const [deleteOpen, setDeleteOpen] = React.useState(false);
  const [deleteLoading, setDeleteLoading] = React.useState(false);
  const formFromAlert = () => ({
    name: alert.name ?? "",
    metric: alert.metric ?? "cpu",
    threshold: alert.threshold ?? 80,
    ratio: alert.ratio ?? 0.8,
    clients: alert.clients || [],
    interval: alert.interval ?? 15,
    mode: (alert.mode as "fixed" | "baseline") || "fixed",
    baseline_days: alert.baseline_days ?? 7,
    multiplier: alert.multiplier ?? 3,
    tasks: (alert.tasks as string[]) || [],
  });
  const [form, setForm] = React.useState(formFromAlert);

  const submitEdit = (update: Record<string, unknown>) => {
    if (saveInFlight.current) return;
    saveInFlight.current = true;
    setEditSaving(true);
    void (async () => {
      try {
        const response = await fetch("/api/admin/notification/load/edit", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            notifications: [
              {
                id: alert.id,
                ...update,
              },
            ],
          }),
        });
        await requireApiSuccess(response, t("common.error"));
        setEditOpen(false);
        toast.success(t("common.updated_successfully"));
      } catch (error) {
        toast.error(error instanceof Error ? error.message : t("common.error"));
      } finally {
        await refresh();
        saveInFlight.current = false;
        setEditSaving(false);
      }
    })();
  };

  // 编辑提交
  const handleEdit = (e: React.FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    submitEdit(form);
  };

  // 删除
  const handleDelete = () => {
    setDeleteLoading(true);
    fetch("/api/admin/notification/load/delete", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id: [alert.id] }),
    })
      .then((res) => {
        if (!res.ok) {
          return res.json().then((data) => {
            throw new Error(data?.message || t("common.error"));
          });
        }
        return res.json();
      })
      .then(() => {
        setDeleteOpen(false);
        toast.success(t("common.deleted_successfully"));
        refresh();
      })
      .catch((error) => {
        toast.error(error.message);
      })
      .finally(() => setDeleteLoading(false));
  };

  return (
    <TableRow key={alert.id} data-testid="load-rule-row">
      <TableCell>{alert.name}</TableCell>
      <TableCell>
        <Flex gap="2" align="center">
          {alert.clients && alert.clients.length > 0
            ? (() => {
                const names = alert.clients.map((uuid) => {
                  const name =
                    nodeDetail.find((node) => node.uuid === uuid)?.name || uuid;
                  return name;
                });
                const joined = names.join(", ");
                return joined.length > 40
                  ? joined.slice(0, 40) + "..."
                  : joined;
              })()
            : t("common.none")}
          <NodeSelectorDialog
            value={alert.clients ?? []}
            hiddenUuidOnlyClient
            onChange={(uuids) => submitEdit({ clients: uuids })}
          >
            <IconButton
              variant="ghost"
              disabled={editSaving}
              title={t("common.select_clients", "Select clients")}
              aria-label={t("common.select_clients", "Select clients")}
            >
              <MoreHorizontal size="16" />
            </IconButton>
          </NodeSelectorDialog>
        </Flex>
      </TableCell>
      <TableCell>{alert.metric?.toUpperCase()}</TableCell>
      <TableCell>
        {alert.mode === "baseline"
          ? `P95 × ${alert.multiplier ?? 3} (${alert.baseline_days ?? 7}d)`
          : `${alert.threshold}%`}
      </TableCell>
      <TableCell>{alert.ratio}</TableCell>
      <TableCell>
        {alert.interval} {t("time.minute")}
      </TableCell>
      <TableCell className="flex items-center gap-2">
        <RulePreview alert={alert} />
        {/* 编辑按钮 */}
        <Dialog.Root open={editOpen} onOpenChange={(open) => {
          if (open) setForm(formFromAlert());
          setEditOpen(open);
        }}>
          <Dialog.Trigger>
            <IconButton
              variant="soft"
              disabled={editSaving}
              title={t("common.edit", "Edit")}
              aria-label={t("common.edit", "Edit")}
            >
              <Pencil size="16" />
            </IconButton>
          </Dialog.Trigger>
          <Dialog.Content data-testid="load-rule-edit">
            <Dialog.Title>{t("common.edit")}</Dialog.Title>
            <form onSubmit={handleEdit} className="flex flex-col gap-2">
              <label>{t("common.name")}</label>
              <TextField.Root
                value={form.name}
                onChange={(e) =>
                  setForm((f) => ({ ...f, name: e.target.value }))
                }
                required
              />
              <label>{t("loadAlert.metric")}</label>
              <Select.Root
                value={form.metric}
                onValueChange={(v) =>
                  setForm((f) => ({ ...f, metric: v as any }))
                }
              >
                <Select.Trigger />
                <Select.Content>
                  <Select.Item value="cpu">CPU</Select.Item>
                  <Select.Item value="ram">RAM</Select.Item>
                  <Select.Item value="disk">Disk</Select.Item>
                  <Select.Item value="net_in">Net In</Select.Item>
                  <Select.Item value="net_out">Net Out</Select.Item>
                  <Select.Item value="load">Load</Select.Item>
                  <Select.Item value="swap">Swap</Select.Item>
                  <Select.Item value="temp">Temp</Select.Item>
                  <Select.Item value="gpu">GPU</Select.Item>
                  <Select.Item value="backup_age">{t("loadAlert.m_backup_age", "Backup age (s)")}</Select.Item>
                  <Select.Item value="backup_ok">{t("loadAlert.m_backup_ok", "Backup ok (1/0)")}</Select.Item>
                  <Select.Item value="ping_loss">{t("loadAlert.m_ping_loss", "Ping loss (%)")}</Select.Item>
                  <Select.Item value="ping_latency">{t("loadAlert.m_ping_latency", "Ping latency (ms)")}</Select.Item>
                </Select.Content>
              </Select.Root>
              <label>{t("loadAlert.threshold_mode", "Threshold mode")}</label>
              <Select.Root
                value={form.mode}
                onValueChange={(v) =>
                  setForm((f) => ({ ...f, mode: v as "fixed" | "baseline" }))
                }
              >
                <Select.Trigger />
                <Select.Content>
                  <Select.Item value="fixed">
                    {t("loadAlert.mode_fixed", "Fixed threshold")}
                  </Select.Item>
                  <Select.Item value="baseline">
                    {t(
                      "loadAlert.mode_baseline",
                      "Baseline (vs its own history)",
                    )}
                  </Select.Item>
                </Select.Content>
              </Select.Root>
              {form.mode === "baseline" && (
                <>
                  <label>
                    {t("loadAlert.baseline_days", "Baseline lookback (days)")}
                  </label>
                  <TextField.Root
                    type="number"
                    min="1"
                    max="365"
                    value={form.baseline_days}
                    onChange={(e) =>
                      setForm((f) => ({
                        ...f,
                        baseline_days: Number(e.target.value),
                      }))
                    }
                  />
                  <label>
                    {t("loadAlert.multiplier", "Alert above N x baseline (P95)")}
                  </label>
                  <TextField.Root
                    type="number"
                    step="0.1"
                    min="1"
                    value={form.multiplier}
                    onChange={(e) =>
                      setForm((f) => ({
                        ...f,
                        multiplier: Number(e.target.value),
                      }))
                    }
                  />
                </>
              )}
              <label>
                {t("common.threshold")} (%)
                {form.mode === "baseline"
                  ? " — " + t("loadAlert.floor_hint", "acts as a floor")
                  : ""}
              </label>
              <TextField.Root
                type="number"
                step="any"
                data-testid="load-rule-threshold"
                value={form.threshold}
                onChange={(e) =>
                  setForm((f) => ({ ...f, threshold: Number(e.target.value) }))
                }
                required
              />
              <label>{t("loadAlert.ratio")}</label>
              <TextField.Root
                type="number"
                step="0.1"
                min="0"
                max="1"
                data-testid="load-rule-ratio"
                value={form.ratio}
                onChange={(e) =>
                  setForm((f) => ({ ...f, ratio: Number(e.target.value) }))
                }
                required
              />
              {isPingMetric(form.metric) && (
                <>
                  <label>{t("loadAlert.ping_tasks", "Ping tasks to watch")}</label>
                  <PingTaskPicker
                    value={form.tasks}
                    onChange={(v) => setForm((f) => ({ ...f, tasks: v }))}
                  />
                </>
              )}
              <label>{t("common.server")}</label>
              <Flex>
                <NodeSelectorDialog
                  value={form.clients}
                  hiddenUuidOnlyClient
                  onChange={(v) => setForm((f) => ({ ...f, clients: v }))}
                />
              </Flex>
              <label>
                {t("ping.interval")} ({t("time.minute")})
              </label>
              <TextField.Root
                type="number"
                value={form.interval}
                onChange={(e) =>
                  setForm((f) => ({ ...f, interval: Number(e.target.value) }))
                }
                required
              />
              <Flex gap="2" justify="end" className="mt-4">
                <Dialog.Close>
                  <Button
                    variant="soft"
                    color="gray"
                    type="button"
                    onClick={() => setEditOpen(false)}
                  >
                    {t("common.cancel")}
                  </Button>
                </Dialog.Close>
                <Button variant="solid" type="submit" disabled={editSaving}>
                  {t("common.save")}
                </Button>
              </Flex>
            </form>
          </Dialog.Content>
        </Dialog.Root>
        {/* 删除按钮 */}
        <Dialog.Root open={deleteOpen} onOpenChange={setDeleteOpen}>
          <Dialog.Trigger>
            <IconButton
              variant="soft"
              color="red"
              title={t("common.delete", "Delete")}
              aria-label={t("common.delete", "Delete")}
            >
              <Trash size="16" />
            </IconButton>
          </Dialog.Trigger>
          <Dialog.Content>
            <Dialog.Title>{t("common.delete")}</Dialog.Title>
            <Flex gap="2" justify="end" className="mt-4">
              <Dialog.Close>
                <Button
                  variant="soft"
                  color="gray"
                  type="button"
                  onClick={() => setDeleteOpen(false)}
                >
                  {t("common.cancel")}
                </Button>
              </Dialog.Close>
              <Button
                variant="solid"
                color="red"
                onClick={handleDelete}
                disabled={deleteLoading}
              >
                {t("common.delete")}
              </Button>
            </Flex>
          </Dialog.Content>
        </Dialog.Root>
      </TableCell>
    </TableRow>
  );
};

type PreviewTarget = {
  client?: string;
  task?: string;
  status: string;
  samples?: number;
  threshold?: number;
  value?: number;
  exceeding?: number;
  required?: number;
  maintenance: boolean;
};

type PreviewResult = {
  evaluated_at: string;
  window_start: string;
  stored_cooldown_active: boolean;
  targets: PreviewTarget[];
};

const RulePreview = ({ alert }: { alert: LoadAlert }) => {
  const { t } = useTranslation();
  const { nodeDetail } = useNodeDetails();
  const [open, setOpen] = React.useState(false);
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState("");
  const [result, setResult] = React.useState<PreviewResult | null>(null);
  const request = React.useRef<AbortController | null>(null);

  React.useEffect(() => () => request.current?.abort(), []);

  const changeOpen = (next: boolean) => {
    setOpen(next);
    if (!next) {
      request.current?.abort();
      request.current = null;
      setLoading(false);
      setResult(null);
      setError("");
    }
  };

  const run = async () => {
    if (!alert.id || request.current) return;
    const controller = new AbortController();
    request.current = controller;
    setLoading(true);
    setResult(null);
    setError("");
    try {
      const response = await fetch("/api/admin/notification/load/preview", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ id: alert.id }),
        signal: controller.signal,
      });
      const body: unknown = await response.json().catch(() => null);
      const envelope = body && typeof body === "object"
        ? body as { status?: unknown; message?: unknown; data?: unknown }
        : null;
      if (!response.ok || envelope?.status !== "success") {
        throw new Error(typeof envelope?.message === "string" && envelope.message
          ? envelope.message : t("loadAlert.preview.error"));
      }
      const data = envelope.data as PreviewResult | undefined;
      if (!data || !Array.isArray(data.targets) || data.targets.length > 50 ||
          typeof data.evaluated_at !== "string" || !Number.isFinite(Date.parse(data.evaluated_at)) ||
          typeof data.window_start !== "string" || !Number.isFinite(Date.parse(data.window_start)) ||
          typeof data.stored_cooldown_active !== "boolean" || !data.targets.every((target) =>
            target && typeof target.status === "string" && typeof target.maintenance === "boolean" &&
            (target.client == null || typeof target.client === "string") &&
            (target.task == null || typeof target.task === "string") &&
            [target.samples, target.threshold, target.value, target.exceeding, target.required].every(
              (value) => value == null || (typeof value === "number" && Number.isFinite(value)),
            ),
          )) {
        throw new Error(t("loadAlert.preview.error"));
      }
      if (request.current === controller) setResult(data);
    } catch (cause) {
      if (request.current === controller && !controller.signal.aborted) {
        setError(cause instanceof Error ? cause.message : t("loadAlert.preview.error"));
      }
    } finally {
      if (request.current === controller) {
        request.current = null;
        setLoading(false);
      }
    }
  };

  return (
    <Dialog.Root open={open} onOpenChange={changeOpen}>
      <Dialog.Trigger>
        <IconButton variant="soft" title={t("loadAlert.preview.button")}
          aria-label={t("loadAlert.preview.button")} disabled={!alert.id}>
          <FlaskConical size="16" />
        </IconButton>
      </Dialog.Trigger>
      <Dialog.Content maxWidth="720px">
        <Dialog.Title>{t("loadAlert.preview.title", { name: alert.name || `#${alert.id}` })}</Dialog.Title>
        <Dialog.Description>{t("loadAlert.preview.description")}</Dialog.Description>
        <p className="text-sm text-muted-foreground mt-2">{t("loadAlert.preview.caveat")}</p>
        <Flex gap="2" align="center" className="mt-4">
          <Button onClick={run} disabled={loading}>{loading ? t("loadAlert.preview.running") : t("loadAlert.preview.run")}</Button>
        </Flex>
        {error && <p role="alert" className="text-red-600 mt-3">{error}</p>}
        {result && <div aria-live="polite" className="mt-4">
          <p className="text-sm">{t("loadAlert.preview.window", {
            start: new Date(result.window_start).toLocaleString(), end: new Date(result.evaluated_at).toLocaleString(),
          })}</p>
          <p className="text-sm mt-1">{t(result.stored_cooldown_active
            ? "loadAlert.preview.cooldown_active" : "loadAlert.preview.cooldown_inactive")}</p>
          {result.targets.length === 0 ? <p className="mt-3">{t("loadAlert.preview.no_targets")}</p> :
            <div className="overflow-x-auto mt-3">
              <Table>
                <TableHeader><TableHead>{t("loadAlert.preview.target")}</TableHead><TableHead>{t("loadAlert.preview.result")}</TableHead><TableHead>{t("loadAlert.preview.details")}</TableHead></TableHeader>
                <TableBody>{result.targets.map((target, index) => <TableRow key={`${target.client ?? ""}-${target.task ?? ""}-${index}`}>
                  <TableCell>{target.client ? nodeDetail.find((node) => node.uuid === target.client)?.name || target.client : "—"}
                    {target.task && <span> · {t("loadAlert.preview.task", { id: target.task })}</span>}</TableCell>
                  <TableCell>{t(`loadAlert.preview.status.${target.status}`, { defaultValue: t("loadAlert.preview.status.unknown") })}
                    {target.maintenance && <span className="block text-amber-700">{t("loadAlert.preview.maintenance")}</span>}</TableCell>
                  <TableCell>{target.samples != null && <div>{t("loadAlert.preview.samples", { value: target.samples })}</div>}
                    {target.threshold != null && <div>{t("loadAlert.preview.threshold", { value: target.threshold })}</div>}
                    {target.value != null && <div>{t("loadAlert.preview.value", { value: target.value })}</div>}
                    {target.exceeding != null && target.required != null && <div>{t("loadAlert.preview.counts", { exceeding: target.exceeding, required: target.required })}</div>}</TableCell>
                </TableRow>)}</TableBody>
              </Table>
            </div>}
        </div>}
        <Flex justify="end" className="mt-4"><Dialog.Close><Button variant="soft">{t("common.close")}</Button></Dialog.Close></Flex>
      </Dialog.Content>
    </Dialog.Root>
  );
};

/** 这两个指标的数据来自延迟监测任务，而不是主机指标。 */
const PING_METRICS = new Set(["ping_latency", "ping_loss"]);
const isPingMetric = (m: string) => PING_METRICS.has(m);

/**
 * PingTaskPicker —— 选择要盯的延迟监测任务。
 * 只在指标选了 ping_latency / ping_loss 时出现：这两个指标的值来自具体任务，
 * 不选任务就无从求值（服务端也会拒绝）。
 */
const PingTaskPicker: React.FC<{
  value: string[];
  onChange: (v: string[]) => void;
}> = ({ value, onChange }) => {
  const { pingTasks } = usePingTask();
  const list = pingTasks || [];
  if (list.length === 0) {
    return (
      <label className="text-sm font-normal text-gray-500">
        还没有延迟监测任务，请先到「延迟监测」里创建。
      </label>
    );
  }
  return (
    <Flex direction="column" gap="1">
      {list.map((task) => {
        const id = String(task.id ?? "");
        const checked = value.includes(id);
        return (
          <label key={id} className="flex items-center gap-2 text-sm font-normal">
            <Checkbox
              checked={checked}
              onCheckedChange={(c: boolean | "indeterminate") =>
                onChange(
                  c === true ? [...value, id] : value.filter((x) => x !== id),
                )
              }
            />
            <span>
              {task.name || `#${id}`}
              <span className="text-gray-500"> · {task.target}</span>
            </span>
          </label>
        );
      })}
    </Flex>
  );
};

const AddButton: React.FC = () => {
  const { t } = useTranslation();
  const [isOpen, setIsOpen] = React.useState(false);
  const [selected, setSelected] = React.useState<string[]>([]);
  const { refresh } = useLoadAlert();
  const [selectedType, setSelectedType] = React.useState<
    | "cpu"
    | "ram"
    | "disk"
    | "net_in"
    | "net_out"
    | "load"
    | "swap"
    | "temp"
    | "gpu"
    | "backup_age"
    | "backup_ok"
  >("cpu");
  const [saving, setSaving] = React.useState(false);
  const [mode, setMode] = React.useState<"fixed" | "baseline">("fixed");
  const [pingTaskIds, setPingTaskIds] = React.useState<string[]>([]);
  const handleSubmit = (e: React.FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const payload = {
      name: e.currentTarget.load_name.value,
      metric: selectedType,
      threshold: parseFloat(e.currentTarget.threshold.value),
      ratio: parseFloat(e.currentTarget.ratio.value),
      clients: selected,
      interval: parseInt(e.currentTarget.interval.value, 10),
      mode,
      baseline_days:
        mode === "baseline"
          ? parseInt(e.currentTarget.baseline_days.value, 10)
          : 0,
      multiplier:
        mode === "baseline" ? parseFloat(e.currentTarget.multiplier.value) : 0,
      tasks: isPingMetric(selectedType) ? pingTaskIds : [],
    };
    setSaving(true);
    fetch("/api/admin/notification/load/add", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
      },
      body: JSON.stringify(payload),
    })
      .then((response) => requireApiSuccess(response, t("common.error")))
      .then(() => {
        setIsOpen(false);
        setSelected([]);
        setSelectedType("cpu");
        setMode("fixed");
        setPingTaskIds([]);
        toast.success(t("common.success"));
        refresh();
      })
      .catch((error) => {
        console.error("Error adding load alert:", error);
        toast.error(error.message);
      })
      .finally(() => {
        setSaving(false);
      });
  };
  return (
    <Dialog.Root open={isOpen} onOpenChange={setIsOpen}>
      <Dialog.Trigger>
        <Button>{t("common.add")}</Button>
      </Dialog.Trigger>
      <Dialog.Content>
        <Dialog.Title>{t("common.add")}</Dialog.Title>
        <form onSubmit={handleSubmit} className="km-notification-load-form">
          <Flex direction="column" justify="end" gap="2" className="font-bold">
            <label htmlFor="load_name">{t("common.name")}</label>
            <TextField.Root id="load_name" name="load_name" />
            <label htmlFor="type">{t("loadAlert.metric")}</label>
            <Select.Root
              value={selectedType}
              onValueChange={(value) =>
                setSelectedType(
                  value as
                    | "cpu"
                    | "ram"
                    | "disk"
                    | "net_in"
                    | "net_out"
                    | "load"
                    | "swap"
                    | "temp"
                    | "gpu"
                    | "backup_age"
                    | "backup_ok",
                )
              }
            >
              <Select.Trigger id="type" name="type" />
              <Select.Content>
                <Select.Item value="cpu">CPU</Select.Item>
                <Select.Item value="ram">RAM</Select.Item>
                <Select.Item value="disk">Disk</Select.Item>
                <Select.Item value="net_in">Net In(Mbps)</Select.Item>
                <Select.Item value="net_out">Net Out(Mbps)</Select.Item>
                <Select.Item value="load">Load</Select.Item>
                <Select.Item value="swap">Swap</Select.Item>
                <Select.Item value="temp">Temp</Select.Item>
                <Select.Item value="gpu">GPU</Select.Item>
                <Select.Item value="backup_age">{t("loadAlert.m_backup_age", "Backup age (s)")}</Select.Item>
                <Select.Item value="backup_ok">{t("loadAlert.m_backup_ok", "Backup ok (1/0)")}</Select.Item>
                <Select.Item value="ping_loss">{t("loadAlert.m_ping_loss", "Ping loss (%)")}</Select.Item>
                <Select.Item value="ping_latency">{t("loadAlert.m_ping_latency", "Ping latency (ms)")}</Select.Item>
              </Select.Content>
            </Select.Root>
            <label htmlFor="mode">
              {t("loadAlert.threshold_mode", "Threshold mode")}
            </label>
            <Select.Root
              value={mode}
              onValueChange={(v) => setMode(v as "fixed" | "baseline")}
            >
              <Select.Trigger id="mode" name="mode" />
              <Select.Content>
                <Select.Item value="fixed">
                  {t("loadAlert.mode_fixed", "Fixed threshold")}
                </Select.Item>
                <Select.Item value="baseline">
                  {t("loadAlert.mode_baseline", "Baseline (vs its own history)")}
                </Select.Item>
              </Select.Content>
            </Select.Root>
            {mode === "baseline" && (
              <>
                <label htmlFor="baseline_days">
                  {t("loadAlert.baseline_days", "Baseline lookback (days)")}
                </label>
                <TextField.Root
                  id="baseline_days"
                  name="baseline_days"
                  type="number"
                  min="1"
                  max="365"
                  defaultValue={7}
                />
                <label htmlFor="multiplier">
                  {t("loadAlert.multiplier", "Alert above N x baseline (P95)")}
                </label>
                <TextField.Root
                  id="multiplier"
                  name="multiplier"
                  type="number"
                  step="0.1"
                  min="1"
                  defaultValue={3}
                />
              </>
            )}
            <label htmlFor="threshold">
              {t("common.threshold")} (%/Mbps)
              {mode === "baseline"
                ? " — " + t("loadAlert.floor_hint", "acts as a floor")
                : ""}
            </label>
            <TextField.Root
              id="threshold"
              name="threshold"
              type="number"
              defaultValue={80}
              step="0.1"
            />
            <label htmlFor="ratio">{t("loadAlert.ratio")}</label>
            <TextField.Root
              id="ratio"
              name="ratio"
              type="number"
              step="0.1"
              min="0"
              max="1"
              defaultValue={0.8}
            />
            {isPingMetric(selectedType) && (
              <>
                <label>{t("loadAlert.ping_tasks", "Ping tasks to watch")}</label>
                <PingTaskPicker value={pingTaskIds} onChange={setPingTaskIds} />
              </>
            )}
            <label htmlFor="select">{t("common.server")}</label>
            <div className="flex items-center justify-start gap-2">
              <NodeSelectorDialog value={selected} onChange={setSelected} />
              <label className="text-md font-normal">
                {t("common.selected", { count: selected.length })}
              </label>
            </div>
            <label htmlFor="interval">
              {t("ping.interval")} ({t("time.minute")})
            </label>
            <TextField.Root
              id="interval"
              name="interval"
              defaultValue={15}
              type="number"
              placeholder="15"
            />
            <div className="flex justify-end gap-2">
              <Dialog.Close>
                <Button variant="soft">{t("common.close")}</Button>
              </Dialog.Close>
              <Button disabled={saving} type="submit">
                {t("common.add")}
              </Button>
            </div>
          </Flex>
        </form>
      </Dialog.Content>
    </Dialog.Root>
  );
};

export default LoadPage;
