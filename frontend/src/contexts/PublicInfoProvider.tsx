import React from "react";
import defaultTheme from "../../komari-theme.json";
import { PublicInfoContext, type ThemeField, type PublicInfo, type Response } from "./PublicInfoContext";


const defaultThemeSettings = Object.fromEntries(
  (
    (defaultTheme.configuration?.data ?? []) as ThemeField[]
  )
    .filter(
      (field) =>
        typeof field.key === "string" &&
        Object.prototype.hasOwnProperty.call(field, "default"),
    )
    .map((field) => [field.key, field.default]),
);

const withThemeDefaults = (publicInfo: PublicInfo): PublicInfo => {
  if (publicInfo.theme !== "default") {
    return publicInfo;
  }

  return {
    ...publicInfo,
    theme_settings: {
      ...defaultThemeSettings,
      ...(publicInfo.theme_settings ?? {}),
    },
  };
};

const requestPublicInfo = async (): Promise<PublicInfo | null> => {
  const response = await fetch("/api/public");
  if (!response.ok) {
    throw new Error("Failed to fetch public info");
  }
  const resp = (await response.json()) as Response;
  return resp && resp.data ? withThemeDefaults(resp.data) : null;
};

export const PublicInfoProvider: React.FC<{ children: React.ReactNode }> = ({
  children,
}) => {
  const [publicInfo, setPublicInfo] = React.useState<PublicInfo | null>(null);
  // 初值就是 true：原 effect 通过 refresh() 在挂载时同步 setIsLoading(true)（不是空操作，
  // 初值 false），把它折叠进初始状态后，isLoading 从首次渲染起就是 true，只去掉了
  // “effect 提交前那一帧仍是 false”的差异。
  const [isLoading, setIsLoading] = React.useState<boolean>(true);
  const [error, setError] = React.useState<string | null>(null);
  //const { call } = useRPC2Call();
  // 公共信息使用public，避免在私有站点的情况下RPC返回401
  // 只负责发起请求并在响应到达后应用结果：函数体内没有同步的 setState，
  // 所以挂载 effect 可以直接调用它。refresh 保留原有的同步写入（error/loading），
  // 供外部调用方使用。
  const loadPublicInfo = React.useCallback(() => {
    requestPublicInfo()
      .then((data) => {
        setPublicInfo(data);
      })
      .catch((err) => {
        setError(err instanceof Error ? err.message : String(err));
      })
      .finally(() => {
        setIsLoading(false);
      });
  }, []);

  const refresh = React.useCallback(async () => {
    setError(null);
    setIsLoading(true);
    await loadPublicInfo();
  }, [loadPublicInfo]);

  React.useEffect(() => {
    // 挂载加载：refresh() 的同步写入（error=null、isLoading=true）与初始状态完全一致
    // （error 初值 null、isLoading 初值 true），因此这里只发起同一次请求；
    // 所有状态写入都发生在响应之后。
    loadPublicInfo();
  }, [loadPublicInfo]);

  return (
    <PublicInfoContext.Provider value={{ publicInfo, isLoading, error, refresh }}>
      {children}
    </PublicInfoContext.Provider>
  );
};
