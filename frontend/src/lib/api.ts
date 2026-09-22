import React from "react";
import { toast } from "sonner";

/**
 * API utility functions for settings management
 */

export interface SettingsResponse {
  sitename: string;
  description: string;
  cors_origin_check_enabled: boolean;
  geo_ip_enabled: boolean;
  geo_ip_provider: string;
  o_auth_provider: string;
  o_auth_enabled: boolean;
  ssrf_protection_enabled: boolean;
  custom_head: string;
  metric_rollup_minute_retention_minutes?: number;
  metric_rollup_five_minute_retention_minutes?: number;
  metric_rollup_hour_retention_hours?: number;
  CreatedAt: string;
  UpdatedAt: string;
  [key: string]: any;
}

type SettingsRestart = {
  guidePath: string;
};

const migrationGuideStatusPath = "/api/admin/database-migration/auth";
const migrationGuidePollInterval = 500;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function settingsRestartFrom(responseData: unknown): SettingsRestart | undefined {
  if (!isRecord(responseData) || !isRecord(responseData.data)) {
    return undefined;
  }

  const { restart_required: restartRequired, guide_path: guidePath } =
    responseData.data;
  if (
    restartRequired !== true ||
    typeof guidePath !== "string" ||
    !guidePath.startsWith("/") ||
    guidePath.startsWith("//")
  ) {
    return undefined;
  }
  return { guidePath };
}

function waitForMigrationGuide(guidePath: string) {
  const check = async () => {
    try {
      const response = await fetch(migrationGuideStatusPath, {
        cache: "no-store",
      });
      const payload: unknown = await response.json();
      if (response.ok && isRecord(payload) && payload.status === "success") {
        window.location.replace(guidePath);
        return;
      }
    } catch {
      // The previous process may still be stopping or the replacement may not be ready.
    }
    window.setTimeout(check, migrationGuidePollInterval);
  };

  window.setTimeout(check, migrationGuidePollInterval);
}

/**
 * Fetch settings from the API
 * @returns Promise containing the settings data
 */
export async function getSettings(): Promise<SettingsResponse> {
  try {
    const response = await fetch("/api/admin/settings");

    if (!response.ok) {
      throw new Error(`HTTP error! status: ${response.status}`);
    }

    const data = await response.json();
    const settingsPayload = data["data"];

    if (
      typeof settingsPayload !== "object" ||
      settingsPayload === null ||
      Array.isArray(settingsPayload)
    ) {
      throw new Error("Invalid settings response payload");
    }

    // Remove database metadata fields that are not needed for UI
    const settings = Object.fromEntries(
      Object.entries(settingsPayload).filter(
        ([key]) => !["CreatedAt", "UpdatedAt", "id"].includes(key),
      ),
    );

    return settings as SettingsResponse;
  } catch (error) {
    console.error("Failed to fetch settings:", error);
    throw error;
  }
}

/**
 * Update settings via the API
 * @param settings - The settings object to update
 * @returns Promise containing the response
 */
export async function updateSettings(
  settings: Partial<SettingsResponse>
): Promise<SettingsRestart | undefined> {
  const response = await fetch("/api/admin/settings", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(settings),
  });

  if (!response.ok) {
    let message = `HTTP error! status: ${response.status}`;

    try {
      const errorData = await response.json();
      if (errorData?.message) {
        message = String(errorData.message);
      }
    } catch {
      // Keep the fallback HTTP status message.
    }

    console.error("Failed to update settings:", message);
    throw new Error(message);
  }

  let responseData: unknown;
  try {
    responseData = await response.json();
  } catch {
    return undefined;
  }

  const restart = settingsRestartFrom(responseData);
  if (restart) {
    waitForMigrationGuide(restart.guidePath);
  } else {
    // Keep the shared settings cache coherent, so a save made through this helper
    // (or through updateSettingsWithToast) is visible to every useSettings()
    // consumer without a reload.
    publishSettingsPatch(settings);
  }
  return restart;
}
export async function updateSettingsWithToast(
  settings: Partial<SettingsResponse>,
  t: (key: string) => string
): Promise<void> {
  try {
    const restart = await updateSettings(settings);
    if (!restart) {
      toast.success(t("settings.settings_saved"));
    }
  } catch (error) {
    toast.error(t("settings.settings_save_failed") + ": " + error);
    throw error;
  }
}

/**
 * Update a single setting field
 * @param key - The setting key to update
 * @param value - The new value for the setting
 * @param currentSettings - The current settings object (to merge with)
 * @returns Promise containing the response
 */
export async function updateSingleSetting<K extends keyof SettingsResponse>(
  key: K,
  value: SettingsResponse[K],
  currentSettings: SettingsResponse
): Promise<SettingsRestart | undefined> {
  const updatedSettings = { ...currentSettings, [key]: value };
  return updateSettings(updatedSettings);
}

const DEFAULT_SETTINGS: SettingsResponse = {
  sitename: "",
  description: "",
  cors_origin_check_enabled: true,
  geo_ip_enabled: false,
  geo_ip_provider: "",
  o_auth_provider: "",
  o_auth_enabled: false,
  ssrf_protection_enabled: false,
  custom_head: "",
  CreatedAt: "",
  UpdatedAt: "",
};

type SettingsStoreState = {
  settings: SettingsResponse;
  // "idle" means nothing has been loaded yet, so consumers still report loading.
  status: "idle" | "loading" | "ready" | "error";
  error: string | null;
};

// One module-level store for every useSettings() consumer: consumers mounting
// together share a single in-flight request, and a consumer mounting after the
// first load reuses the cached value instead of refetching.
// See docs/OPTIMIZATION-REVIEW-2026-09-22.md (C7b).
let settingsStoreState: SettingsStoreState = {
  settings: DEFAULT_SETTINGS,
  status: "idle",
  error: null,
};

let settingsRequest: Promise<SettingsResponse> | null = null;
const settingsSubscribers = new Set<() => void>();

const getSettingsSnapshot = () => settingsStoreState;

const subscribeToSettings = (onStoreChange: () => void) => {
  settingsSubscribers.add(onStoreChange);
  return () => {
    settingsSubscribers.delete(onStoreChange);
  };
};

const publishSettings = (next: SettingsStoreState) => {
  settingsStoreState = next;
  settingsSubscribers.forEach((onStoreChange) => onStoreChange());
};

const toSettingsErrorMessage = (error: unknown) =>
  error instanceof Error ? error.message : "Failed to fetch settings";

const publishSettingsValue = (settings: SettingsResponse) => {
  publishSettings({
    settings,
    // A store that has never loaded must keep reporting loading, otherwise
    // consumers would render the defaults as if they were real settings.
    status: settingsStoreState.status === "idle" ? "idle" : "ready",
    error: null,
  });
};

const publishSettingsPatch = (patch: Partial<SettingsResponse>) => {
  publishSettingsValue({ ...settingsStoreState.settings, ...patch });
};

/**
 * Fetch settings once for every consumer. Concurrent callers share the request
 * already in flight, and the result stays cached for later mounts.
 */
const requestSettings = (): Promise<SettingsResponse> => {
  if (settingsRequest) {
    return settingsRequest;
  }

  publishSettings({ ...settingsStoreState, status: "loading", error: null });

  settingsRequest = getSettings()
    .then((data) => {
      publishSettings({ settings: data, status: "ready", error: null });
      return data;
    })
    .catch((error: unknown) => {
      publishSettings({
        ...settingsStoreState,
        status: "error",
        error: toSettingsErrorMessage(error),
      });
      throw error;
    })
    .finally(() => {
      settingsRequest = null;
    });

  return settingsRequest;
};

const ensureSettingsLoaded = () => {
  if (settingsStoreState.status === "ready" || settingsRequest) {
    return;
  }
  void requestSettings().catch(() => {
    // The failure is already published as `error`.
  });
};

/**
 * Drop the cached settings and reload them for every consumer. Call this at a
 * session boundary (login/logout) so a later render cannot reuse the settings of
 * the previous session.
 */
export function invalidateSettingsCache(): Promise<void> {
  settingsStoreState = {
    settings: DEFAULT_SETTINGS,
    status: "idle",
    error: null,
  };
  return requestSettings().then(
    () => undefined,
    () => undefined
  );
}

/**
 * Hook for managing settings state and API calls
 */
export function useSettings() {
  const store = React.useSyncExternalStore(
    subscribeToSettings,
    getSettingsSnapshot,
    getSettingsSnapshot
  );

  // A mount only loads when nothing has been loaded yet; a cached value is reused.
  React.useEffect(() => {
    ensureSettingsLoaded();
  }, []);

  const setSettings = React.useCallback(
    (value: React.SetStateAction<SettingsResponse>) => {
      const nextSettings =
        typeof value === "function"
          ? (value as (previous: SettingsResponse) => SettingsResponse)(
              settingsStoreState.settings
            )
          : value;
      publishSettingsValue(nextSettings);
    },
    []
  );

  // Update a single setting
  const updateSetting = React.useCallback(
    async <K extends keyof SettingsResponse>(
      key: K,
      value: SettingsResponse[K]
    ) => {
      try {
        const restart = await updateSingleSetting(
          key,
          value,
          settingsStoreState.settings
        );
        if (!restart) {
          publishSettingsPatch({ [key]: value });
        }
        return restart;
      } catch (err) {
        publishSettings({
          ...settingsStoreState,
          status: "error",
          error: toSettingsErrorMessage(err),
        });
        throw err;
      }
    },
    []
  );

  // Update multiple settings
  const updateMultipleSettings = React.useCallback(
    async (newSettings: Partial<SettingsResponse>) => {
      try {
        const updatedSettings = {
          ...settingsStoreState.settings,
          ...newSettings,
        };
        const restart = await updateSettings(updatedSettings);
        if (!restart) {
          publishSettingsValue(updatedSettings);
        }
        return restart;
      } catch (err) {
        publishSettings({
          ...settingsStoreState,
          status: "error",
          error: toSettingsErrorMessage(err),
        });
        throw err;
      }
    },
    []
  );

  // Refetch for every consumer, not just the caller. Errors still reach the caller.
  const refetch = React.useCallback(async () => {
    const data = await getSettings();
    publishSettings({ settings: data, status: "ready", error: null });
  }, []);

  return {
    settings: store.settings,
    loading: store.status === "idle" || store.status === "loading",
    error: store.error,
    setSettings,
    updateSetting,
    updateMultipleSettings,
    refetch,
  };
}
