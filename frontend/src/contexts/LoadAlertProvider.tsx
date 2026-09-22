import React from "react";
import { LoadAlertContext, type LoadAlert, type Response } from "./LoadAlertContext";


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
