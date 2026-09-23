import React from "react";
import { NotificationContext, type OfflineNotification } from "./NotificationContext";

const requestOfflineNotifications = async (): Promise<OfflineNotification[]> => {
  const response = await fetch("/api/admin/notification/offline");
  if (!response.ok) {
    throw new Error("Failed to fetch offline notifications");
  }
  const data = await response.json();
  return data.data || [];
};

export const OfflineNotificationProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const [offlineNotification, setOfflineNotification] = React.useState<OfflineNotification[]>([]);
  // 初值就是 true：原 effect 通过 refresh() 在挂载时同步 setLoading(true)（不是空操作，
  // 初值 false），把它折叠进初始状态后，loading 从首次渲染起就是 true，只去掉了
  // “effect 提交前那一帧仍是 false”的差异。
  const [loading, setLoading] = React.useState<boolean>(true);
  const firstLoad = React.useRef(true);
  const [error, setError] = React.useState<Error | null>(null);

  // 只负责发起请求并在响应到达后应用结果：函数体内没有同步的 setState，
  // 所以挂载 effect 可以直接调用它。refresh 保留原有的首次加载语义，
  // 供外部调用方使用。
  const loadOfflineNotification = () => {
    requestOfflineNotifications()
      .then((data) => {
        setOfflineNotification(data);
      })
      .catch((err) => {
        console.error("Error fetching offline notifications:", err);
        setError(err instanceof Error ? err : new Error(String(err)));
      })
      .finally(() => {
        if (firstLoad.current) {
          setLoading(false);
          firstLoad.current = false;
        }
      });
  };

  const refresh = async () => {
    if (firstLoad.current) setLoading(true);
    await loadOfflineNotification();
  };

  React.useEffect(() => {
    // 挂载加载：refresh() 的同步写入 setLoading(true) 已折叠进初始状态（见上），
    // 因此这里只发起同一次请求；其余状态写入都发生在响应之后。
    void loadOfflineNotification();
  }, []);

  return (
    <NotificationContext.Provider value={{ offlineNotification, refresh, loading, error }}>
      {children}
    </NotificationContext.Provider>
  );
}
