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

interface Response {
  data: LoadAlert[];
  message: string;
  status: string;
  [property: string]: any;
}

interface LoadAlertContextType {
  loadAlerts: LoadAlert[] | null;
  isLoading: boolean;
  error: string | null;
  refresh: () => void;
}

const LoadAlertContext = React.createContext<LoadAlertContextType | undefined>(
  undefined
);

export const LoadAlertProvider: React.FC<{ children: React.ReactNode }> = ({
  children,
}) => {
  const [loadAlerts, setLoadAlerts] = React.useState<LoadAlert[] | null>(null);
  const [isLoading, setIsLoading] = React.useState<boolean>(false);
  const [error, setError] = React.useState<string | null>(null);

  const refresh = () => {
    setError(null);
    fetch("/api/admin/notification/load")
      .then((response) => {
        if (!response.ok) {
          throw new Error("Failed to fetch notification tasks");
        }
        return response.json();
      })
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

  React.useEffect(() => {
    setIsLoading(true);

    refresh();
    setIsLoading(false);
  }, []);

  return (
    <LoadAlertContext.Provider value={{ loadAlerts, isLoading, error, refresh }}>
      {children}
    </LoadAlertContext.Provider>
  );
};

export const useLoadAlert = () => {
  const context = React.useContext(LoadAlertContext);
  if (!context) {
    throw new Error("useLoadAlert must be used within a LoadAlertProvider");
  }
  return context;
};
