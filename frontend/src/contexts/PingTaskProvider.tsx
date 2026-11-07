import React from "react";
import { PingTaskContext, type PingTask, type Response } from "./PingTaskContext";
import { requireApiSuccess } from "@/lib/apiResponse";

const requestPingTasks = () =>
  fetch("/api/admin/ping").then((response) => {
    if (!response.ok) {
      throw new Error("Failed to fetch ping tasks");
    }
    return response.json();
  });

export const PingTaskProvider: React.FC<{
  children: React.ReactNode;
}> = ({ children }) => {
  const [pingTasks, setPingTasks] = React.useState<PingTask[] | null>(null);
  const [isLoading, setIsLoading] = React.useState<boolean>(false);
  const [error, setError] = React.useState<string | null>(null);
  const [schedulePending, setSchedulePending] = React.useState<boolean | null>(null);
  const requestGeneration = React.useRef(0);

  // 只负责发起请求并在响应到达后应用结果：函数体内没有同步的 setState，
  // 所以挂载 effect 可以直接调用它。refresh 保留原有的同步写入（error），
  // 供外部调用方使用。
  const loadPingTasks = () => {
    const generation = ++requestGeneration.current;
    requestPingTasks()
      .then((resp: Response) => {
        if (generation !== requestGeneration.current) return;
        if (resp && Array.isArray(resp.data)) {
          setPingTasks(resp.data);
        } else {
          setPingTasks([]);
        }
      })
      .catch((err) => {
        if (generation === requestGeneration.current) {
          setError(err.message || "An error occurred while fetching ping tasks");
        }
      })
      .finally(() => {
        if (generation === requestGeneration.current) setIsLoading(false);
      });
    fetch("/api/admin/ping/schedule-state")
      .then((response) => requireApiSuccess<{ reload_pending: boolean }>(response))
      .then((response) => {
        if (generation === requestGeneration.current) {
          setSchedulePending(typeof response.data?.reload_pending === "boolean" ? response.data.reload_pending : null);
        }
      })
      .catch(() => {
        if (generation === requestGeneration.current) setSchedulePending(null);
      });
  };

  const refresh = () => {
    setError(null);
    setSchedulePending(null);
    loadPingTasks();
  };

  React.useEffect(() => {
    // 挂载加载：原 effect 的 setIsLoading(true)/setIsLoading(false) 在同一次 effect
    // flush 内相互抵消（isLoading 初值就是 false），refresh() 的 setError(null) 也与
    // 初值相同，因此这里只发起同一次请求；所有状态写入都发生在响应之后。
    const generation = requestGeneration;
    loadPingTasks();
    return () => { generation.current++; };
  }, []);

  return (
    <PingTaskContext.Provider value={{ pingTasks, isLoading, error, schedulePending, refresh }}>
      {children}
    </PingTaskContext.Provider>
  );
};
