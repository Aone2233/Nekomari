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
  const latestRequest = React.useRef<{ promise: Promise<void> } | null>(null);

  // Superseded refreshes wait for the newest result before unlocking editors.
  const loadLoadAlerts = () => {
    const request = { promise: Promise.resolve() };
    request.promise = requestLoadAlerts()
      .then((resp: Response) => {
        if (request !== latestRequest.current) return;
        if (resp && Array.isArray(resp.data)) {
          setError(null);
          setLoadAlerts(resp.data);
        } else {
          setLoadAlerts([]);
        }
      })
      .catch((err) => {
        if (request === latestRequest.current) {
          setError(err.message || "An error occurred while fetching load alerts");
        }
      })
      .finally(() => {
        if (request === latestRequest.current) setIsLoading(false);
      });
    latestRequest.current = request;
    return (async () => {
      let pending = request;
      for (;;) {
        await pending.promise;
        if (latestRequest.current === pending || latestRequest.current === null) return;
        pending = latestRequest.current;
      }
    })();
  };

  const refresh = () => {
    setError(null);
    return loadLoadAlerts();
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
