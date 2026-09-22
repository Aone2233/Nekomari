import React from "react";

export interface PingTask {
  clients?: string[];
  default_on?: boolean;
  id?: number;
  interval?: number;
  target?: string;
  // reference 可选：路径归因用的参考目标（典型填本机网关）
  reference?: string;
  type?: string;
  [property: string]: any;
}
export interface Response {
  data: PingTask[];
  message: string;
  status: string;
  [property: string]: any;
}
export interface PingTaskContextType {
  pingTasks: PingTask[] | null;
  isLoading: boolean;
  error: string | null;
  refresh: () => void;
}
export const PingTaskContext = React.createContext<PingTaskContextType | undefined>(
  undefined
);

export const usePingTask = () => {
  const context = React.useContext(PingTaskContext);
  if (!context) {
    throw new Error("usePingTask must be used within a PingTaskProvider");
  }
  return context;
};
