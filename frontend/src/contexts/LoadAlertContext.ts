import React from "react";

export interface LoadAlert {
  id?: number;
  name?: string;
  clients?: string[];
  /** ping 指标要盯的延迟监测任务 id */
  tasks?: string[];
  metric?: "cpu" | "ram" | "disk" | "net_in" | "net_out" | "load" | "swap" | "temp" | "gpu" | "backup_age" | "backup_ok";
  threshold?: number;
  ratio?: number;
  interval?: number;
  last_notified?: string;
  /** fixed = 用固定阈值；baseline = 与自身历史基线比较 */
  mode?: "fixed" | "baseline";
  baseline_days?: number;
  multiplier?: number;
  [property: string]: any;
}
export interface Response {
  data: LoadAlert[];
  message: string;
  status: string;
  [property: string]: any;
}
export interface LoadAlertContextType {
  loadAlerts: LoadAlert[] | null;
  isLoading: boolean;
  error: string | null;
  refresh: () => void;
}
export const LoadAlertContext = React.createContext<LoadAlertContextType | undefined>(
  undefined
);

export const useLoadAlert = () => {
  const context = React.useContext(LoadAlertContext);
  if (!context) {
    throw new Error("useLoadAlert must be used within a LoadAlertProvider");
  }
  return context;
};
