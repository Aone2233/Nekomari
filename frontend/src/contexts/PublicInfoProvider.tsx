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

export const PublicInfoProvider: React.FC<{ children: React.ReactNode }> = ({
  children,
}) => {
  const [publicInfo, setPublicInfo] = React.useState<PublicInfo | null>(null);
  const [isLoading, setIsLoading] = React.useState<boolean>(false);
  const [error, setError] = React.useState<string | null>(null);
  //const { call } = useRPC2Call();
  // 公共信息使用public，避免在私有站点的情况下RPC返回401
  const refresh = React.useCallback(async () => {
    setError(null);
    setIsLoading(true);
    try {
      const response = await fetch("/api/public");
      if (!response.ok) {
        throw new Error("Failed to fetch public info");
      }
      const resp = (await response.json()) as Response;
      if (resp && resp.data) {
        setPublicInfo(withThemeDefaults(resp.data));
      } else {
        setPublicInfo(null);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setIsLoading(false);
    }
  }, []);

  React.useEffect(() => {
    void refresh();
  }, [refresh]);

  return (
    <PublicInfoContext.Provider value={{ publicInfo, isLoading, error, refresh }}>
      {children}
    </PublicInfoContext.Provider>
  );
};
