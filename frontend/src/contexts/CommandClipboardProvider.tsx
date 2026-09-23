import React from "react";
import { CommandClipboardContext, type CommandClipboard } from "./CommandClipboardContext";

const requestCommands = async (): Promise<CommandClipboard[]> => {
  const response = await fetch("/api/admin/clipboard");
  if (!response.ok) {
    throw new Error("Failed to fetch commands");
  }
  const resp = await response.json();
  return resp && Array.isArray(resp.data) ? resp.data : [];
};

export const CommandClipboardProvider: React.FC<{
  children: React.ReactNode;
}> = ({ children }) => {
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState<Error | null>(null);
  const [commands, setCommands] = React.useState<CommandClipboard[]>([]);
  // 只负责发起请求并在响应到达后应用结果：函数体内没有同步的 setState，
  // 所以挂载 effect 可以直接调用它。refresh 保留原有的同步写入（loading/error），
  // 供外部调用方使用。
  const loadCommands = () =>
    requestCommands()
      .then((data) => {
        setCommands(data);
      })
      .catch((err) => {
        setError(err as Error);
      })
      .finally(() => {
        setLoading(false);
      });

  const refresh = async () => {
    setLoading(true);
    setError(null);
    await loadCommands();
  };
  const addCommand = async (name: string, text: string, remark: string, weight: number) => {
    try {
      const response = await fetch("/api/admin/clipboard", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify({ name, text, remark, weight }),
      });
      if (!response.ok) {
        throw new Error("Failed to add command");
      }
      refresh();
    } catch (err) {
      setError(err as Error);
    } finally {
      setLoading(false);
    }
  };

  const updateCommand = async (
    id: number,
    name: string,
    text: string,
    remark: string,
    weight: number
  ) => {
    try {
      const response = await fetch(`/api/admin/clipboard/${id}`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify({ name, text, remark, weight }),
      });
      if (!response.ok) {
        throw new Error("Failed to update command");
      }
      refresh();
    } catch (err) {
      setError(err as Error);
    } finally {
      setLoading(false);
    }
  };

  const deleteCommand = async (id: number) => {
    try {
      const response = await fetch(`/api/admin/clipboard/${id}/remove`, {
        method: "POST",
      });
      if (!response.ok) {
        throw new Error("Failed to delete command");
      }
      refresh();
    } catch (err) {
      setError(err as Error);
    } finally {
      setLoading(false);
    }
  };

  React.useEffect(() => {
    // 挂载加载：refresh() 的同步写入（loading=true、error=null）与初始状态完全一致
    // （loading 初值 true、error 初值 null），因此这里只发起同一次请求；
    // 所有状态写入都发生在响应之后。
    void loadCommands();
  }, []);
  return (
    <CommandClipboardContext.Provider
      value={{
        commands,
        loading,
        error,
        refresh,
        addCommand,
        updateCommand,
        deleteCommand,
      }}
    >
      {children}
    </CommandClipboardContext.Provider>
  );
};
