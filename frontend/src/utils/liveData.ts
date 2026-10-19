import type { LiveDataResponse, MaintenanceStatus, Record as LiveRecord } from "../types/LiveData";

export const UNKNOWN_LIVE_RECORD: LiveRecord = {
  cpu: { usage: null }, ram: { used: null }, swap: { used: null },
  load: { load1: null, load5: null, load15: null }, disk: { used: null },
  network: { up: null, down: null, totalUp: null, totalDown: null },
  connections: { tcp: null, udp: null }, uptime: null, process: null,
  message: "", updated_at: "",
};

/**
 * The maintenance field, kept as an object whose presence means "inside a window".
 *
 * Two decisions live here. An empty object is kept rather than dropped, because a window whose
 * name and reason are blank is still a window and the card still has to say so. And the value is
 * passed through without a clock comparison: the server decided whether the node is inside a
 * window at the instant it built the response, so the client never has to — a local clock a minute
 * off would otherwise flicker the badge at the edges of every window.
 */
function normalizeMaintenance(value: unknown): MaintenanceStatus | undefined {
  if (!value || typeof value !== "object") return undefined;
  const source = value as { [key: string]: unknown };
  const status: MaintenanceStatus = {};
  const text = (key: "name" | "reason" | "until") => {
    const field = source[key];
    if (typeof field !== "string" || field.trim() === "") return;
    status[key] = field;
  };
  text("name");
  text("reason");
  text("until");
  return status;
}

export function usagePercent(used: number | null, total: number | null | undefined): number | null {
  return used !== null && typeof total === "number" && total > 0 ? used / total * 100 : null;
}

export function compareNullable(a: number | null, b: number | null, descending: boolean): number {
  if (a === null) return b === null ? 0 : 1;
  if (b === null) return -1;
  return descending ? b - a : a - b;
}

export function mergeLiveData(result: Record<string, any>, previous: LiveDataResponse | null): LiveDataResponse {
  const nextOnline = Object.values(result).filter(value => value?.online).map(value => value.client as string);
  const previousData = previous?.data;
  const online = previousData && previousData.online.length === nextOnline.length &&
    previousData.online.every((value, index) => value === nextOnline[index]) ? previousData.online : nextOnline;
  const data: Record<string, LiveRecord> = {};
  let changed = !previousData || online !== previousData.online;
  for (const [uuid, record] of Object.entries(result)) {
    const measured = (key: string, value: unknown): number | null => {
      const quality = record.quality?.[key];
      return (!quality || quality === "ok") && typeof value === "number" && Number.isFinite(value) ? value : null;
    };
    const totalConnections = measured("connections", record.connections);
    const udp = measured("connections", record.connections_udp);
    const tcp = record.connections_tcp !== undefined ? measured("connections", record.connections_tcp) :
      totalConnections !== null && udp !== null && totalConnections >= udp ? totalConnections - udp : null;
    const gpu = measured("gpu", record.gpu_average_usage ?? record.gpu);
    const nextRecord: LiveRecord = {
      cpu: { usage: measured("cpu", record.cpu) },
      ram: { used: measured("ram", record.ram), total: measured("ram", record.ram_total) },
      swap: { used: measured("swap", record.swap), total: measured("swap", record.swap_total) },
      load: { load1: measured("load", record.load), load5: measured("load", record.load5), load15: measured("load", record.load15) },
      disk: { used: measured("disk", record.disk), total: measured("disk", record.disk_total) },
      network: {
        up: measured("net_rate", record.net_out), down: measured("net_rate", record.net_in),
        totalUp: measured("network", record.net_total_out ?? record.net_total_up),
        totalDown: measured("network", record.net_total_in ?? record.net_total_down),
      },
      connections: { tcp, udp },
      gpu: gpu === null ? undefined : { count: record.gpu_count ?? 0, average_usage: gpu, detailed_info: record.gpu_detailed_info ?? [] },
      uptime: measured("uptime", record.uptime), process: measured("process", record.process),
      quality: record.quality, sampled_at: record.sampled_at, received_at: record.received_at,
      counter_epoch: record.counter_epoch, sample_interval_seconds: record.sample_interval_seconds,
      message: "", updated_at: record.time ?? "",
      // 只有在窗口内服务端才带这个对象；下一次响应没有它，记录就会被重建，徽标随之消失
      // （JSON.stringify 比较会看到这个字段从有到无）。
      maintenance: normalizeMaintenance(record.maintenance),
    };
    const old = previousData?.data[uuid];
    if (old && JSON.stringify(old) === JSON.stringify(nextRecord)) data[uuid] = old;
    else { data[uuid] = nextRecord; changed = true; }
  }
  if (previousData && Object.keys(previousData.data).length !== Object.keys(data).length) changed = true;
  return !changed && previous ? previous : { data: { online, data }, status: "ok" };
}
