import {
  Button,
  Callout,
  Dialog,
  Flex,
  SegmentedControl,
  Text,
} from "@radix-ui/themes";
import { AlertTriangle, Download, Eye, LoaderCircle, RefreshCw } from "lucide-react";
import React from "react";
import { useTranslation } from "react-i18next";
import { z } from "zod";
import { SettingCard, SettingCardLabel } from "@/components/admin/SettingCard";
import { formatBytes } from "@/utils/unitHelper";
import { formatUptime } from "@/utils/timeFormat";

const pprofProfileNameSchema = z.enum([
  "cpu",
  "trace",
  "allocs",
  "block",
  "goroutine",
  "heap",
  "mutex",
  "threadcreate",
]);
const pprofEndpointSchema = z.string().refine(
  (value) => value.startsWith("/api/admin/pprof/"),
  "invalid pprof endpoint",
);
const pprofProfileSchema = z.object({
  name: pprofProfileNameSchema,
  endpoint: pprofEndpointSchema,
  samples: z.number().int().nonnegative().optional(),
  timed: z.boolean().optional(),
  preview: pprofEndpointSchema.optional(),
});
// A sql.DB pool snapshot. The keys are the wire field names: this page is a diagnostics view,
// and five new locale keys to translate "open / in_use / idle / wait" would buy nothing that the
// numbers underneath do not already say. See poolText for how they are presented.
const pprofDbPoolSchema = z.object({
  open: z.number().int().nonnegative(),
  in_use: z.number().int().nonnegative(),
  idle: z.number().int().nonnegative(),
  wait_count: z.number().int().nonnegative(),
  wait_duration_ms: z.number().finite().nonnegative(),
});
// The metric store reports its write pool with those same five fields — one renderer for both
// rows — and nests a dedicated read pool under "read" when it has one. The nesting is
// meaningful: no read pool means reads go through the write pool, which is a different fact
// from a read pool that happens to have no connections open.
const pprofMetricsDbSchema = pprofDbPoolSchema.extend({
  read: pprofDbPoolSchema.optional(),
});
const pprofThemeCacheSchema = z.object({
  disk: z.string(),
  memory_loaded: z.boolean(),
});
const pprofSummarySchema = z.object({
  profiles: z.array(pprofProfileSchema),
  runtime: z.object({
    goroutines: z.number().int().nonnegative(),
    memory: z.object({
      heap_alloc: z.number().finite().nonnegative(),
      heap_inuse: z.number().finite().nonnegative(),
      heap_objects: z.number().int().nonnegative(),
      sys: z.number().finite().nonnegative(),
    }),
    // Added after the page shipped, so all four are optional: a panel still on the older server
    // must keep rendering this page instead of failing the whole response as "invalid summary".
    // rss_bytes is 0 on platforms where the server cannot read it, which is "unavailable" rather
    // than "zero bytes" — the renderer hides it instead of printing "0 B".
    rss_bytes: z.number().finite().nonnegative().optional(),
    uptime_seconds: z.number().finite().nonnegative().optional(),
    db_pool: pprofDbPoolSchema.optional(),
    metrics_db: pprofMetricsDbSchema.optional(),
    theme_cache: pprofThemeCacheSchema.optional(),
  }),
  duration: z.object({
    default_seconds: z.number().int().min(1).max(30),
    min_seconds: z.number().int().min(1).max(30),
    max_seconds: z.number().int().min(1).max(30),
  }),
});
const pprofSummaryResponseSchema = z.object({
  status: z.literal("success"),
  data: pprofSummarySchema,
});

type PprofProfile = z.infer<typeof pprofProfileSchema>;
type PprofSummary = z.infer<typeof pprofSummarySchema>;
type PprofDbPool = z.infer<typeof pprofDbPoolSchema>;
type PprofPreview = {
  profile: PprofProfile;
  text: string;
};

const knownThemeCacheStates = new Set(["disabled", "uninitialized", "initializing", "ready", "failed", "degraded"]);

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function captureDurations(summary: PprofSummary): number[] {
  return Array.from(
    new Set([
      summary.duration.min_seconds,
      summary.duration.default_seconds,
      summary.duration.max_seconds,
    ]),
  ).sort((left, right) => left - right);
}

function pprofDownloadURL(profile: PprofProfile, seconds: number): string {
  if (!profile.timed) return profile.endpoint;

  const url = new URL(profile.endpoint, window.location.origin);
  url.searchParams.set("seconds", String(seconds));
  return url.pathname + url.search;
}

function pprofFilename(profile: PprofProfile): string {
  return profile.name === "trace" ? "trace.out" : `${profile.name}.pprof`;
}

async function responseError(response: Response, fallback: string): Promise<string> {
  try {
    const payload: unknown = await response.json();
    if (isRecord(payload) && typeof payload.message === "string") {
      return payload.message;
    }
  } catch {
    // Diagnostic downloads are binary on success and may be plain text on errors.
  }
  return `${fallback} (${response.status})`;
}

/**
 * poolText renders one connection pool.
 *
 * The state words are the wire field names on purpose (see pprofDbPoolSchema): the labels this
 * page shows are data, and the one thing a reader must not have to do here is map a translated
 * sentence back onto the field they are debugging. `wait` is included only when something
 * actually waited, so a healthy pool stays three numbers wide.
 */
function poolText(pool: PprofDbPool): string {
  const parts = [`open ${pool.open}`, `in_use ${pool.in_use}`, `idle ${pool.idle}`];
  if (pool.wait_count > 0) {
    parts.push(`wait ${pool.wait_count} (${Math.round(pool.wait_duration_ms)} ms)`);
  }
  return parts.join(" · ");
}

function OverviewMetric({
  label,
  value,
  tone,
  testId,
}: {
  label: string;
  value: string;
  /** Amber marks a figure that deserves a second look (someone waited on a connection). */
  tone?: "amber";
  testId?: string;
}) {
  return (
    <div
      className="min-w-0 border-b border-[var(--gray-a5)] py-3 last:border-b-0"
      data-testid={testId}
    >
      <Text as="div" size="1" color="gray">
        {label}
      </Text>
      <Text as="div" size="3" weight="medium" className="break-words" color={tone}>
        {value}
      </Text>
    </div>
  );
}

/** One connection pool row: same renderer for the main database and every metric-store pool. */
function PoolMetric({ label, pool, testId }: { label: string; pool: PprofDbPool; testId: string }) {
  return (
    <OverviewMetric
      label={label}
      value={poolText(pool)}
      testId={testId}
      // 有连接真的等过（wait_count>0）才标黄：空闲池是常态，标黄会把常态噪声化。
      tone={pool.wait_count > 0 ? "amber" : undefined}
    />
  );
}

function ProfileRow({
  profile,
  busy,
  activeAction,
  onPreview,
  onDownload,
}: {
  profile: PprofProfile;
  busy: boolean;
  activeAction: string | null;
  onPreview: (profile: PprofProfile) => void;
  onDownload: (profile: PprofProfile) => void;
}) {
  const { t } = useTranslation();
  const previewing = activeAction === `${profile.name}:preview`;
  const downloading = activeAction === `${profile.name}:download`;
  const profileKey = `pprof.profiles.${profile.name}`;

  return (
    <div className="grid grid-cols-1 gap-3 border-b border-[var(--gray-a5)] py-3 last:border-b-0 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center">
      <div className="min-w-0">
        <Text as="div" size="2" weight="medium">
          {t(`${profileKey}.title`)}
        </Text>
        <Text as="div" size="1" color="gray" className="break-words">
          {t(`${profileKey}.description`)}
        </Text>
        <Text as="div" size="1" color="gray" className="pt-1">
          {profile.timed
            ? t("pprof.timed_profile")
            : t("pprof.sample_count", { samples: profile.samples ?? 0 })}
        </Text>
      </div>
      <Flex gap="2" wrap="wrap" className="sm:justify-end">
        {profile.preview ? (
          <Button
            variant="soft"
            disabled={busy}
            onClick={() => onPreview(profile)}
          >
            {previewing ? (
              <LoaderCircle size={16} className="animate-spin" />
            ) : (
              <Eye size={16} />
            )}
            {previewing ? t("pprof.loading") : t("pprof.view")}
          </Button>
        ) : null}
        <Button
          variant="soft"
          disabled={busy}
          onClick={() => onDownload(profile)}
        >
          {downloading ? (
            <LoaderCircle size={16} className="animate-spin" />
          ) : (
            <Download size={16} />
          )}
          {downloading ? t("pprof.loading") : t("pprof.download")}
        </Button>
      </Flex>
    </div>
  );
}

export default function PprofPage() {
  const { t } = useTranslation();
  const [summary, setSummary] = React.useState<PprofSummary | null>(null);
  const [summaryLoading, setSummaryLoading] = React.useState(true);
  const [summaryError, setSummaryError] = React.useState<string | null>(null);
  const [captureSeconds, setCaptureSeconds] = React.useState(10);
  const [activeAction, setActiveAction] = React.useState<string | null>(null);
  const [actionError, setActionError] = React.useState<string | null>(null);
  const [preview, setPreview] = React.useState<PprofPreview | null>(null);
  const [heapPreview, setHeapPreview] = React.useState<string | null>(null);
  const [heapPreviewLoading, setHeapPreviewLoading] = React.useState(false);
  const [heapPreviewError, setHeapPreviewError] = React.useState<string | null>(null);
  const heapPreviewRequestRef = React.useRef(0);

  const readProfilePreview = React.useCallback(
    async (profile: PprofProfile): Promise<string> => {
      if (!profile.preview) {
        throw new Error(t("pprof.preview_unavailable"));
      }

      const response = await fetch(profile.preview, {
        cache: "no-store",
        credentials: "same-origin",
        headers: { Accept: "text/plain" },
      });
      if (!response.ok) {
        throw new Error(await responseError(response, t("pprof.request_failed")));
      }
      return response.text();
    },
    [t],
  );

  const loadHeapPreview = React.useCallback(
    async (profile: PprofProfile | undefined) => {
      const requestID = ++heapPreviewRequestRef.current;
      if (!profile?.preview) {
        setHeapPreview(null);
        setHeapPreviewError(null);
        setHeapPreviewLoading(false);
        return;
      }

      setHeapPreviewLoading(true);
      setHeapPreviewError(null);
      try {
        const text = await readProfilePreview(profile);
        if (requestID === heapPreviewRequestRef.current) {
          setHeapPreview(text);
        }
      } catch (cause) {
        if (requestID === heapPreviewRequestRef.current) {
          setHeapPreview(null);
          setHeapPreviewError(cause instanceof Error ? cause.message : String(cause));
        }
      } finally {
        if (requestID === heapPreviewRequestRef.current) {
          setHeapPreviewLoading(false);
        }
      }
    },
    [readProfilePreview],
  );

  // Pure request: resolves to the summary, or throws a display-ready error.
  const requestSummary = React.useCallback(async (): Promise<PprofSummary> => {
    const response = await fetch("/api/admin/pprof/summary", {
      cache: "no-store",
      credentials: "same-origin",
    });
    if (!response.ok) {
      throw new Error(await responseError(response, t("pprof.summary_load_failed")));
    }

    const parsed = pprofSummaryResponseSchema.safeParse(await response.json());
    if (!parsed.success) {
      throw new Error(t("pprof.invalid_summary"));
    }

    return parsed.data.data;
  }, [t]);

  const applySummary = React.useCallback(
    (data: PprofSummary) => {
      setSummary(data);
      setSummaryError(null);
      void loadHeapPreview(
        data.profiles.find((profile) => profile.name === "heap"),
      );
      setCaptureSeconds((current) => {
        const options = captureDurations(data);
        return options.includes(current)
          ? current
          : data.duration.default_seconds;
      });
    },
    [loadHeapPreview],
  );

  const applySummaryFailure = React.useCallback((cause: unknown) => {
    setSummary(null);
    setSummaryError(cause instanceof Error ? cause.message : String(cause));
  }, []);

  const loadSummary = React.useCallback(async () => {
    setSummaryLoading(true);
    setSummaryError(null);
    heapPreviewRequestRef.current++;
    setHeapPreview(null);
    setHeapPreviewError(null);
    setHeapPreviewLoading(false);
    try {
      const data = await requestSummary();
      applySummary(data);
    } catch (cause) {
      applySummaryFailure(cause);
    } finally {
      setSummaryLoading(false);
    }
  }, [applySummary, applySummaryFailure, requestSummary]);

  React.useEffect(() => {
    let active = true;
    // Drop any heap preview still in flight for the previous summary.
    heapPreviewRequestRef.current++;
    const load = async () => {
      try {
        const data = await requestSummary();
        if (!active) return;
        applySummary(data);
      } catch (cause) {
        if (!active) return;
        applySummaryFailure(cause);
      } finally {
        if (active) setSummaryLoading(false);
      }
    };
    void load();
    return () => {
      active = false;
    };
  }, [applySummary, applySummaryFailure, requestSummary]);

  const downloadProfile = async (profile: PprofProfile) => {
    setActiveAction(`${profile.name}:download`);
    setActionError(null);
    try {
      const response = await fetch(pprofDownloadURL(profile, captureSeconds), {
        cache: "no-store",
        credentials: "same-origin",
        headers: { Accept: "application/octet-stream" },
      });
      if (!response.ok) {
        throw new Error(await responseError(response, t("pprof.request_failed")));
      }

      const url = URL.createObjectURL(await response.blob());
      const link = document.createElement("a");
      link.href = url;
      link.download = pprofFilename(profile);
      document.body.append(link);
      link.click();
      link.remove();
      window.setTimeout(() => URL.revokeObjectURL(url), 0);
    } catch (cause) {
      setActionError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setActiveAction(null);
    }
  };

  const previewProfile = async (profile: PprofProfile) => {
    if (profile.name === "heap" && heapPreview !== null) {
      setActionError(null);
      setPreview({ profile, text: heapPreview });
      return;
    }

    setActiveAction(`${profile.name}:preview`);
    setActionError(null);
    try {
      setPreview({ profile, text: await readProfilePreview(profile) });
    } catch (cause) {
      setActionError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setActiveAction(null);
    }
  };

  const busy = activeAction !== null;
  const durationOptions = summary ? captureDurations(summary) : [];
  // 指标库把写池的五个字段平铺，独立读池嵌在 read 下（契约见后端 pprofMetricDatabasePool）。
  // read 存在时两行都要有后缀：只标 read 会让另一行看起来是"全部"，而它只是写池。
  const metricsDb = summary?.runtime.metrics_db;
  const themeCache = summary?.runtime.theme_cache;
  const themeCacheState = themeCache && knownThemeCacheStates.has(themeCache.disk) ? themeCache.disk : "unknown";

  return (
    <Flex direction="column" gap="3" className="km-page-admin-pprof">
      <Flex justify="between" align="center" gap="2" wrap="wrap">
        <SettingCardLabel>{t("pprof.title")}</SettingCardLabel>
        <Button
          variant="soft"
          disabled={summaryLoading}
          onClick={() => void loadSummary()}
        >
          <RefreshCw size={16} className={summaryLoading ? "animate-spin" : undefined} />
          {t("common.refresh")}
        </Button>
      </Flex>

      <Callout.Root color="orange" variant="surface">
        <Callout.Icon>
          <AlertTriangle size={16} />
        </Callout.Icon>
        <Callout.Text>{t("pprof.warning")}</Callout.Text>
      </Callout.Root>

      {summaryError ? (
        <Callout.Root color="red" variant="surface">
          <Callout.Icon>
            <AlertTriangle size={16} />
          </Callout.Icon>
          <Callout.Text>{summaryError}</Callout.Text>
        </Callout.Root>
      ) : null}

      {actionError ? (
        <Callout.Root color="red" variant="surface">
          <Callout.Icon>
            <AlertTriangle size={16} />
          </Callout.Icon>
          <Callout.Text>{actionError}</Callout.Text>
        </Callout.Root>
      ) : null}

      <SettingCard
        title={t("pprof.runtime_overview_title")}
        description={t("pprof.runtime_overview_description")}
      >
        {summary ? (
          <Flex direction="column" gap="3" className="w-full">
            <div className="grid w-full grid-cols-1 gap-x-6 sm:grid-cols-2 lg:grid-cols-5">
              <OverviewMetric
                label={t("pprof.goroutines")}
                value={summary.runtime.goroutines.toLocaleString()}
              />
              <OverviewMetric
                label={t("pprof.heap_alloc")}
                value={formatBytes(summary.runtime.memory.heap_alloc)}
              />
              <OverviewMetric
                label={t("pprof.heap_inuse")}
                value={formatBytes(summary.runtime.memory.heap_inuse)}
              />
              <OverviewMetric
                label={t("pprof.heap_objects")}
                value={summary.runtime.memory.heap_objects.toLocaleString()}
              />
              <OverviewMetric
                label={t("pprof.runtime_sys")}
                value={formatBytes(summary.runtime.memory.sys)}
              />
              {/* "RSS" has no locale key and does not need one: it is the acronym admins use, and
                  0 means the server could not read it (non-Linux), not that the process is empty. */}
              {summary.runtime.rss_bytes ? (
                <OverviewMetric
                  label="RSS"
                  value={formatBytes(summary.runtime.rss_bytes)}
                  testId="pprof-metric-rss"
                />
              ) : null}
              {summary.runtime.uptime_seconds !== undefined ? (
                <OverviewMetric
                  label={t("nodeCard.uptime")}
                  value={formatUptime(summary.runtime.uptime_seconds, t)}
                  testId="pprof-metric-uptime"
                />
              ) : null}
            </div>
            {summary.runtime.db_pool || metricsDb ? (
              <div className="grid w-full grid-cols-1 gap-x-6 sm:grid-cols-2 lg:grid-cols-3">
                {summary.runtime.db_pool ? (
                  <PoolMetric
                    label={t("settings.database.main")}
                    pool={summary.runtime.db_pool}
                    testId="pprof-db-pool"
                  />
                ) : null}
                {metricsDb ? (
                  <PoolMetric
                    label={
                      metricsDb.read
                        ? `${t("settings.database.monitoring")} (write)`
                        : t("settings.database.monitoring")
                    }
                    pool={metricsDb}
                    testId="pprof-metrics-db-pool"
                  />
                ) : null}
                {metricsDb?.read ? (
                  <PoolMetric
                    label={`${t("settings.database.monitoring")} (read)`}
                    pool={metricsDb.read}
                    testId="pprof-metrics-db-read-pool"
                  />
                ) : null}
              </div>
            ) : null}
            {themeCache ? (
              <div>
                <div className="grid w-full grid-cols-1 gap-x-6 sm:grid-cols-2">
                  <OverviewMetric
                    label={t("pprof.theme_cache_disk")}
                    value={t(`pprof.theme_cache_disk_${themeCacheState}`)}
                    tone={themeCacheState === "failed" || themeCacheState === "degraded" ? "amber" : undefined}
                    testId="pprof-theme-cache-disk"
                  />
                  <OverviewMetric
                    label={t("pprof.theme_cache_memory")}
                    value={t(themeCache.memory_loaded ? "pprof.theme_cache_memory_loaded" : "pprof.theme_cache_memory_not_loaded")}
                    testId="pprof-theme-cache-memory"
                  />
                </div>
                <Text as="p" size="1" color="gray">{t("pprof.theme_cache_note")}</Text>
              </div>
            ) : null}
          </Flex>
        ) : (
          <Text size="2" color="gray" className="w-full py-3">
            {summaryLoading ? t("loading") : t("pprof.summary_unavailable")}
          </Text>
        )}
      </SettingCard>

      {summary ? (
        <SettingCard
          title={t("pprof.heap_preview_title")}
          description={t("pprof.heap_preview_description")}
          className="km-pprof-output"
        >
          {heapPreviewLoading ? (
            <Text size="2" color="gray" className="w-full py-3">
              {t("pprof.loading")}
            </Text>
          ) : heapPreview !== null ? (
            <pre className="max-h-[32vh] w-full overflow-auto rounded-md border border-[var(--gray-a5)] p-3 text-xs">
              {heapPreview}
            </pre>
          ) : (
            <Text
              size="2"
              color={heapPreviewError ? "red" : "gray"}
              className="w-full py-3 break-words"
            >
              {heapPreviewError ?? t("pprof.preview_unavailable")}
            </Text>
          )}
        </SettingCard>
      ) : null}

      {summary ? (
        <SettingCard
          title={t("pprof.profiles_title")}
          description={t("pprof.profiles_description")}
          className="km-pprof-form"
        >
          <Flex direction="column" gap="3" className="w-full pt-3">
            <Flex direction="column" gap="1">
              <Text size="2" weight="medium">
                {t("pprof.capture_duration")}
              </Text>
              <SegmentedControl.Root
                value={String(captureSeconds)}
                onValueChange={(value) => {
                  const seconds = Number(value);
                  if (durationOptions.includes(seconds)) {
                    setCaptureSeconds(seconds);
                  }
                }}
                size="1"
              >
                {durationOptions.map((seconds) => (
                  <SegmentedControl.Item key={seconds} value={String(seconds)}>
                    {t("pprof.duration_option", { seconds })}
                  </SegmentedControl.Item>
                ))}
              </SegmentedControl.Root>
            </Flex>

            <Flex direction="column" gap="0">
              {summary.profiles.map((profile) => (
                <ProfileRow
                  key={profile.name}
                  profile={profile}
                  busy={busy}
                  activeAction={activeAction}
                  onPreview={previewProfile}
                  onDownload={downloadProfile}
                />
              ))}
            </Flex>
          </Flex>
        </SettingCard>
      ) : null}

      <Dialog.Root
        open={preview !== null}
        onOpenChange={(open) => {
          if (!open) setPreview(null);
        }}
      >
        <Dialog.Content maxWidth="960px">
          <Dialog.Title>
            {preview
              ? t("pprof.preview_title", {
                  profile: t(`pprof.profiles.${preview.profile.name}.title`),
                })
              : t("pprof.title")}
          </Dialog.Title>
          <Dialog.Description size="2">
            {t("pprof.preview_description")}
          </Dialog.Description>
          <pre className="mt-3 max-h-[65vh] overflow-auto rounded-md border border-[var(--gray-a5)] p-3 text-xs">
            {preview?.text}
          </pre>
          <Flex justify="end" mt="3">
            <Dialog.Close>
              <Button variant="soft" color="gray">
                {t("common.close")}
              </Button>
            </Dialog.Close>
          </Flex>
        </Dialog.Content>
      </Dialog.Root>
    </Flex>
  );
}
