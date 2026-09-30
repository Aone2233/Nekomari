export type LiveData = {
    online: string[];
    data: { [key: string]: Record };
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
};

export type LiveDataResponse = {
  data: LiveData;
  status: string;
};
