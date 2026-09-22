import React from 'react';
import { NodeDetailsContext, type NodeDetail } from "./NodeDetailsContext";

export const NodeDetailsProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const [nodeDetail, setNodeDetail] = React.useState<NodeDetail[] | []>([]);
  const [isLoading, setIsLoading] = React.useState<boolean>(false);
  const [error, setError] = React.useState<string | null>(null);

  // useCallback keeps `refresh` referentially stable: consumers use it in effect
  // dependency arrays (e.g. the 5s polling effect in pages/admin/index.tsx) and a
  // function recreated on every provider render would tear those timers down and
  // rebuild them on every poll response.
  const refresh = React.useCallback(() => {
    fetch("/api/admin/client/list")
      .then((response) => response.json())
      .then((data: NodeDetail[]) => {
        setNodeDetail(data);
        setIsLoading(false);
      })
      .catch((error) => {
        setError(error.message);
        setIsLoading(false);
      });
  }, []);
    React.useEffect(() => {
        setIsLoading(true);
        refresh();
        // refresh 是 useCallback([]) 的稳定引用，所以这里仍然只在挂载时跑一次。
    }, [refresh]);
  return (
    <NodeDetailsContext.Provider value={{ nodeDetail, isLoading, error, refresh }}>
      {children}
    </NodeDetailsContext.Provider>
  );
};
