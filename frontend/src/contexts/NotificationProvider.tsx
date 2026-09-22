import React from "react";
import { NotificationContext, type OfflineNotification } from "./NotificationContext";


export const OfflineNotificationProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const [offlineNotification, setOfflineNotification] = React.useState<OfflineNotification[]>([]);
  const [loading, setLoading] = React.useState<boolean>(false);
  const firstLoad = React.useRef(true);
  const [error, setError] = React.useState<Error | null>(null);

  const refresh = async () => {
    if (firstLoad.current) setLoading(true);
    try {
      const response = await fetch("/api/admin/notification/offline");
      if (!response.ok) {
        throw new Error("Failed to fetch offline notifications");
      }
      const data = await response.json();
      setOfflineNotification(data.data || []);
    } catch (error) {
      console.error("Error fetching offline notifications:", error);
      setError(error instanceof Error ? error : new Error(String(error)));
    } finally {
      if (firstLoad.current) {
        setLoading(false);
        firstLoad.current = false;
      }
    }
  };

  React.useEffect(() => {
    refresh();
  }, []);

  return (
    <NotificationContext.Provider value={{ offlineNotification, refresh, loading, error }}>
      {children}
    </NotificationContext.Provider>
  );
}
