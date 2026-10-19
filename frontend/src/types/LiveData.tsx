export type LiveData = {
    online: string[];
    data: { [key: string]: Record };
};

/**
 * One node's maintenance-window state at the moment the status was assembled
 * (common:getNodesLatestStatus, field `maintenance`).
 *
 * The object is present only while the node is inside a window — that presence is the
 * signal, not a flag inside it: a window may carry no name and no reason, and "in a window"
 * still has to be sayable. `until` is the window's end as an RFC3339 instant, derived by the
 * server; the front-end formats it for display and never decides the window itself, because a
 * client clock a minute off would make the badge flicker at the edges.
 */
export type MaintenanceStatus = {
  name?: string;
  reason?: string;
  until?: string;
};

export type Record = {
  cpu: {
    usage: number | null;
  };
  ram: {
    used: number | null;
    total?: number | null;
  };
  swap: {
    used: number | null;
    total?: number | null;
  };
  load: {
    load1: number | null;
    load5: number | null;
    load15: number | null;
  };
  disk: {
    used: number | null;
    total?: number | null;
  };
  network: {
    up: number | null;
    down: number | null;
    totalUp: number | null;
    totalDown: number | null;
  };
  connections: {
    tcp: number | null;
    udp: number | null;
  };
  gpu?: {
    count: number;
    average_usage: number;
    detailed_info: {
      name: string;
      memory_total: number;
      memory_used: number;
      utilization: number;
      temperature: number;
    }[];
  };
  uptime: number | null;
  process: number | null;
  quality?: { [key: string]: string };
  sampled_at?: string;
  received_at?: string;
  counter_epoch?: string;
  sample_interval_seconds?: number;
  message: string;
  updated_at: string;
  maintenance?: MaintenanceStatus;
};

export type LiveDataResponse = {
  data: LiveData;
  status: string;
};
