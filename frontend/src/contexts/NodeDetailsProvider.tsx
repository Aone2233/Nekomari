import React from 'react';
import { NodeDetailsContext, type NodeDetail } from "./NodeDetailsContext";

export const NodeDetailsProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const [nodeDetail, setNodeDetail] = React.useState<NodeDetail[] | []>([]);
  // 初值就是 true：原 effect 在挂载时无条件 setIsLoading(true)（不是空操作，初值 false），
  // 把它折叠进初始状态后，isLoading 从首次渲染起就是 true，只去掉了“effect 提交前
  // 那一帧仍是 false”的差异。
  const [isLoading, setIsLoading] = React.useState<boolean>(true);
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
        // 挂载加载：原来的 setIsLoading(true) 已折叠进初始状态（见上），
        // 这里只保留 refresh 调用，因此 effect 内不再有同步的 setState。
        // refresh 是 useCallback([]) 的稳定引用，所以这里仍然只在挂载时跑一次。
        refresh();
    }, [refresh]);
  return (
    <NodeDetailsContext.Provider value={{ nodeDetail, isLoading, error, refresh }}>
      {children}
    </NodeDetailsContext.Provider>
  );
};
