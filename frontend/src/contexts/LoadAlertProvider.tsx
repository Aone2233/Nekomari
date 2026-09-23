import React from "react";
import { LoadAlertContext, type LoadAlert, type Response } from "./LoadAlertContext";

const requestLoadAlerts = () =>
  fetch("/api/admin/notification/load").then((response) => {
    if (!response.ok) {
      throw new Error("Failed to fetch notification tasks");
    }
    return response.json();
  });

export const LoadAlertProvider: React.FC<{ children: React.ReactNode }> = ({
  children,
}) => {
  const [loadAlerts, setLoadAlerts] = React.useState<LoadAlert[] | null>(null);
  const [isLoading, setIsLoading] = React.useState<boolean>(false);
  const [error, setError] = React.useState<string | null>(null);

  // 只负责发起请求并在响应到达后应用结果：函数体内没有同步的 setState，
  // 所以挂载 effect 可以直接调用它。refresh 保留原有的同步写入（error），
  // 供外部调用方使用。
  const loadLoadAlerts = () => {
    requestLoadAlerts()
      .then((resp: Response) => {
        if (resp && Array.isArray(resp.data)) {
          setLoadAlerts(resp.data);
        } else {
          setLoadAlerts([]);
        }
      })
      .catch((err) => {
        setError(err.message || "An error occurred while fetching load alerts");
      })
      .finally(() => {
        setIsLoading(false);
      });
  };

  const refresh = () => {
    setError(null);
    loadLoadAlerts();
  };

  React.useEffect(() => {
    // 挂载加载：原 effect 的 setIsLoading(true)/setIsLoading(false) 在同一次 effect
    // flush 内相互抵消（isLoading 初值就是 false），refresh() 的 setError(null) 也与
    // 初值相同，因此这里只发起同一次请求；所有状态写入都发生在响应之后。
    loadLoadAlerts();
  }, []);

  return (
    <LoadAlertContext.Provider value={{ loadAlerts, isLoading, error, refresh }}>
      {children}
    </LoadAlertContext.Provider>
  );
};
