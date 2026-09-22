import React from "react";


export type NodeBasicInfo = {
  /** 节点唯一标识符 */
  uuid: string;
  /** 节点名称 */
  name: string;
  /** CPU型号 */
  cpu_name: string;
  /** 虚拟化 */
  virtualization: string;
  /** 系统架构 */
  arch: string;
  /** CPU核心数 */
  cpu_cores: number;
  /** 操作系统 */
  os: string;
  /** 内核版本 */
  kernel_version: string;
  /** GPU型号 */
  gpu_name: string;
  /** 地区标识 */
  region: string;
  /** 总内存(字节) */
  mem_total: number;
  /** 总交换空间(字节) */
  swap_total: number;
  /** 总磁盘空间(字节) */
  disk_total: number;
  /** 版本号 */
  version: string;
  /** 权重 */
  weight: number;
  /** 价格 */
  price: number;
  tags: string;
  /** 账单周期（天）*/
  billing_cycle: number;
  /** 货币 */
  currency: string;
  /** 分组 */
  group: string;
  /** 流量阈值 */
  traffic_limit: number;
  /** 流量阈值类型 */
  traffic_limit_type: undefined | "sum" | "max" | "min" | "up" | "down";
  /** 过期时间 */
  expired_at: string;
  /** 创建时间 */
  created_at: string;
  /** 更新时间 */
  updated_at: string;
  ipv4?: string; 
  ipv6?: string;
};
export interface NodeListContextType {
  nodeList: NodeBasicInfo[] | null;
  isLoading: boolean;
  error: string | null;
  refresh: () => void;
}

const NODE_LIST_CONTEXT_KEY = "__komariNodeListContext" as const;
export type NodeListContextGlobal = typeof globalThis & {
  [NODE_LIST_CONTEXT_KEY]?: React.Context<NodeListContextType | undefined>;
};

const globalNodeListContext = globalThis as NodeListContextGlobal;
export const NodeListContext =
  globalNodeListContext[NODE_LIST_CONTEXT_KEY] ??
  (globalNodeListContext[NODE_LIST_CONTEXT_KEY] =
    React.createContext<NodeListContextType | undefined>(undefined));

export function useNodeList(): NodeListContextType;
export function useNodeList(required: false): NodeListContextType | undefined;
export function useNodeList(required = true) {
  const context = React.useContext(NodeListContext);
  if (!context && required) {
    throw new Error("useNodeList must be used within a NodeListProvider");
  }
  return context;
}
