export const CHUNK_SIZE = 5 * 1024 * 1024;
// Archive storage serializes writes and finalization to bound temporary disk use.
const WORKER_COUNT = 1;
// Every budget here is finite. A permanent rejection must fail on its first response,
// and a stuck request must fail the upload instead of pinning the dialog forever.
const MAX_CHUNK_ATTEMPTS = 4;
const MAX_CANCEL_ATTEMPTS = 3;
const BASE_RETRY_DELAY_MS = 1_000;
// Retry-After is honoured up to this ceiling; waiting longer than this would freeze the
// upload UI, and the operator can always start the upload again once the store frees up.
const MAX_RETRY_DELAY_MS = 30_000;
const CHUNK_TIMEOUT_MS = 120_000;
const INIT_TIMEOUT_MS = 30_000;
// Merge copies every chunk and then runs the installer/restorer, so it needs a longer
// deadline than init; five minutes still keeps a hung finalization observable.
const MERGE_TIMEOUT_MS = 300_000;
const CANCEL_TIMEOUT_MS = 15_000;
// web/upload/chunk.go expires a session after 24 hours (the Store.ttl default). The
// client repeats that number only to tell the operator when an unreleased reservation
// disappears by itself.
const SESSION_TTL_MS = 24 * 60 * 60 * 1000;

export type UploadPurpose = "backup" | "plugin" | "theme";

type InitResponse = {
  status?: string;
  message?: string;
  data?: {
    upload_id?: unknown;
    chunk_size?: unknown;
  };
};

type MergeResponse<T> = {
  status?: string;
  message?: string;
  data?: T;
};

export type CancelFailure = {
  cancelled: false;
  /** "busy": the store kept answering 429. "rejected": the request itself is invalid.
   *  "unreachable": no usable response (network error, deadline, 5xx). */
  reason: "busy" | "rejected" | "unreachable";
  message: string;
  /** Upper bound until the server reclaims the reservation on its own. */
  ttlMs: number;
};

export type CancelOutcome = { cancelled: true } | CancelFailure;

export type ChunkUploadTask = {
  upload<T>(
    purpose: UploadPurpose,
    file: File,
    onProgress: (progress: number) => void,
  ): Promise<T | undefined>;
  /**
   * Aborts the local transfer immediately and releases the server-side reservation.
   * Resolves instead of rejecting: the four call sites invoke `cancel()` as a
   * fire-and-forget statement, so a rejection would surface as an unhandled rejection
   * that nobody reads, which is the silent loss this replaces. Callers that want the
   * TTL fallback notice should await the result and show `message` when
   * `cancelled` is false.
   */
  cancel(): Promise<CancelOutcome>;
};

/**
 * The merge request left the client but no response came back. The server may already
 * have installed the archive, so callers must not report this as "nothing happened"
 * and must never replay the merge.
 */
export class MergeOutcomeUnknownError extends Error {
  readonly uncertain = true;

  constructor(message: string) {
    super(message);
    this.name = "MergeOutcomeUnknownError";
  }
}

class RequestDeadlineError extends Error {
  constructor(label: string, timeoutMs: number) {
    super(`${label} timed out after ${timeoutMs}ms`);
    this.name = "RequestDeadlineError";
  }
}

class ChunkRequestError extends Error {
  readonly status: number;
  readonly transient: boolean;
  readonly retryAfterMs: number | undefined;

  constructor(status: number, message: string, retryAfterMs?: number) {
    super(message);
    this.name = "ChunkRequestError";
    this.status = status;
    this.transient = isTransientStatus(status);
    this.retryAfterMs = retryAfterMs;
  }
}

/**
 * Parses Retry-After as delta-seconds or an HTTP-date. The caller clamps the result;
 * this only rejects values that carry no usable delay. `undefined` is accepted because
 * header lookups differ between XHR (null) and Headers (null or missing).
 */
export function parseRetryAfter(
  value: string | null | undefined,
  now: number = Date.now(),
): number | undefined {
  if (value === null || value === undefined) return undefined;
  const trimmed = value.trim();
  if (trimmed === "") return undefined;
  if (/^\d+$/.test(trimmed)) return Number(trimmed) * 1000;
  const at = Date.parse(trimmed);
  if (Number.isNaN(at)) return undefined;
  return Math.max(0, at - now);
}

// 0 means "no HTTP response at all" (network error or deadline): a retry is the only
// way to learn whether the server is reachable. 4xx other than 408/429 is the server
// refusing this payload and will never become true by repeating it.
//
// 507 Insufficient Storage is the one 5xx that is not transient: web/upload answers it
// when the filesystem cannot absorb this upload's worst-case footprint, and repeating
// the chunk a second later cannot change that. The store's transient refusal is 429
// (another write or finalization holds the store lock, with Retry-After), so everything
// else in the 5xx range stays retryable as a genuine server fault.
function isTransientStatus(status: number): boolean {
  return status === 0 || status === 408 || status === 429 || (status >= 500 && status !== 507);
}

function boundedDelay(requestedMs: number | undefined, attempt: number): number {
  return Math.min(
    requestedMs ?? BASE_RETRY_DELAY_MS * 2 ** attempt,
    MAX_RETRY_DELAY_MS,
  );
}

function ttlNotice(): string {
  return `the server will reclaim the reservation automatically within ${SESSION_TTL_MS / 3_600_000} hours`;
}

function serverMessage(body: string): string | undefined {
  try {
    const payload = JSON.parse(body) as { message?: unknown };
    if (typeof payload.message === "string") return payload.message;
  } catch {
    // Keep the HTTP status when the response is not the API envelope.
  }
  return undefined;
}

function apiMessage(payload: { message?: string }, status: number): string {
  return payload.message || `HTTP ${status}`;
}

async function readJson<T>(response: Response): Promise<T> {
  try {
    return (await response.json()) as T;
  } catch {
    // A proxy or an error page can answer with HTML; the caller falls back to the status.
    return {} as T;
  }
}

export function createChunkUploadTask(basePath: string): ChunkUploadTask {
  const controller = new AbortController();
  const activeXhrs = new Set<XMLHttpRequest>();
  let uploadID = "";
  let cancelled = false;
  let cancelOutcome: Promise<CancelOutcome> | null = null;

  const abortError = () => new DOMException("Upload cancelled", "AbortError");

  // Chunk retries stop as soon as the caller cancels.
  const sleepWhileActive = (ms: number) =>
    new Promise<void>((resolve, reject) => {
      const onAbort = () => {
        clearTimeout(timer);
        reject(abortError());
      };
      const timer = setTimeout(() => {
        controller.signal.removeEventListener("abort", onAbort);
        resolve();
      }, ms);
      controller.signal.addEventListener("abort", onAbort, { once: true });
    });

  // Cancel retries run after the task controller is already aborted, so this sleep
  // deliberately ignores that signal.
  const sleep = (ms: number) =>
    new Promise<void>((resolve) => setTimeout(resolve, ms));

  // Bounds one fetch: the local controller carries both the caller's cancellation and
  // the deadline, and `timedOut` keeps the two apart when the request fails.
  const request = async (
    label: string,
    timeoutMs: number,
    send: (signal: AbortSignal) => Promise<Response>,
    followCancel: boolean,
  ): Promise<Response> => {
    const local = new AbortController();
    let timedOut = false;
    const forwardAbort = () => local.abort();
    if (followCancel && !controller.signal.aborted) {
      controller.signal.addEventListener("abort", forwardAbort, { once: true });
    }
    const timer = setTimeout(() => {
      timedOut = true;
      local.abort();
    }, timeoutMs);
    try {
      return await send(local.signal);
    } catch (error) {
      if (timedOut) throw new RequestDeadlineError(label, timeoutMs);
      if (followCancel && controller.signal.aborted) throw abortError();
      throw error;
    } finally {
      clearTimeout(timer);
      controller.signal.removeEventListener("abort", forwardAbort);
    }
  };

  const cancelSession = async (id: string): Promise<CancelOutcome> => {
    let failure: CancelFailure = {
      cancelled: false,
      reason: "unreachable",
      message: "cancel upload failed",
      ttlMs: SESSION_TTL_MS,
    };
    let retryAfterMs: number | undefined;
    for (let attempt = 0; attempt < MAX_CANCEL_ATTEMPTS; attempt += 1) {
      if (attempt > 0) await sleep(boundedDelay(retryAfterMs, attempt - 1));
      try {
        const response = await request(
          "upload cancel",
          CANCEL_TIMEOUT_MS,
          (signal) =>
            fetch(`${basePath}/cancel`, {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({ upload_id: id }),
              signal,
            }),
          false,
        );
        // 404 means the session is already gone (merged or expired): nothing lingers,
        // so this counts as a successful cancellation.
        if (response.ok || response.status === 404) return { cancelled: true };
        const message = apiMessage(
          await readJson<{ message?: string }>(response),
          response.status,
        );
        if (response.status === 429) {
          // The store is briefly locked by another write or finalization.
          retryAfterMs = parseRetryAfter(response.headers.get("Retry-After"));
          failure = {
            cancelled: false,
            reason: "busy",
            message,
            ttlMs: SESSION_TTL_MS,
          };
          continue;
        }
        if (!isTransientStatus(response.status)) {
          // A malformed cancel request stays malformed; retrying only delays the notice.
          return {
            cancelled: false,
            reason: "rejected",
            message,
            ttlMs: SESSION_TTL_MS,
          };
        }
        retryAfterMs = parseRetryAfter(response.headers.get("Retry-After"));
        failure = {
          cancelled: false,
          reason: "unreachable",
          message,
          ttlMs: SESSION_TTL_MS,
        };
      } catch (error) {
        retryAfterMs = undefined;
        failure = {
          cancelled: false,
          reason: "unreachable",
          message: error instanceof Error ? error.message : String(error),
          ttlMs: SESSION_TTL_MS,
        };
      }
    }
    return { ...failure, message: `${failure.message}; ${ttlNotice()}` };
  };

  const cancel = (): Promise<CancelOutcome> => {
    if (cancelOutcome) return cancelOutcome;
    cancelled = true;
    controller.abort();
    for (const xhr of activeXhrs) xhr.abort();
    const id = uploadID;
    uploadID = "";
    // Without a known session there is nothing to release; an init response lost to
    // cancellation leaves at most one empty reservation, which the TTL reclaims.
    cancelOutcome = id
      ? cancelSession(id)
      : Promise.resolve<CancelOutcome>({ cancelled: true });
    return cancelOutcome;
  };

  const uploadChunk = (
    id: string,
    index: number,
    chunk: Blob,
    onProgress: (loaded: number) => void,
  ): Promise<void> =>
    new Promise((resolve, reject) => {
      const xhr = new XMLHttpRequest();
      activeXhrs.add(xhr);
      const finish = (callback: () => void) => {
        activeXhrs.delete(xhr);
        callback();
      };

      xhr.upload.addEventListener("progress", (event) => {
        if (event.lengthComputable) onProgress(event.loaded);
      });
      xhr.addEventListener("load", () => {
        if (xhr.status >= 200 && xhr.status < 300) {
          finish(resolve);
          return;
        }
        finish(() =>
          reject(
            new ChunkRequestError(
              xhr.status,
              serverMessage(xhr.responseText) ??
                `chunk ${index} upload failed: ${xhr.status}`,
              parseRetryAfter(xhr.getResponseHeader("Retry-After")),
            ),
          ),
        );
      });
      xhr.addEventListener("error", () =>
        finish(() =>
          reject(new ChunkRequestError(0, `chunk ${index} upload failed`)),
        ),
      );
      xhr.addEventListener("timeout", () =>
        finish(() =>
          reject(new ChunkRequestError(0, `chunk ${index} upload timed out`)),
        ),
      );
      xhr.addEventListener("abort", () => finish(() => reject(abortError())));

      const form = new FormData();
      form.append("upload_id", id);
      form.append("chunk_index", String(index));
      form.append("chunk_data", chunk, `chunk-${index}`);
      xhr.open("POST", `${basePath}/chunk`);
      xhr.timeout = CHUNK_TIMEOUT_MS;
      xhr.send(form);
    });

  return {
    cancel,
    async upload<T>(
      purpose: UploadPurpose,
      file: File,
      onProgress: (progress: number) => void,
    ): Promise<T | undefined> {
      if (cancelled) throw abortError();
      try {
        // Init is never retried: each attempt creates a session, and a lost response
        // would leave an extra reservation behind until the TTL.
        const initResponse = await request(
          "upload init",
          INIT_TIMEOUT_MS,
          (signal) =>
            fetch(`${basePath}/init`, {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({
                purpose,
                size: file.size,
                filename: file.name,
              }),
              signal,
            }),
          true,
        );
        const initPayload = await readJson<InitResponse>(initResponse);
        if (!initResponse.ok || initPayload.status !== "success") {
          throw new Error(apiMessage(initPayload, initResponse.status));
        }
        const id = initPayload.data?.upload_id;
        const chunkSize = initPayload.data?.chunk_size;
        if (typeof id !== "string" || chunkSize !== CHUNK_SIZE) {
          throw new Error("Invalid chunk upload configuration");
        }
        uploadID = id;

        const totalChunks = Math.ceil(file.size / CHUNK_SIZE);
        const progress = new Map<number, number>();
        let nextChunk = 0;
        const updateProgress = () => {
          let uploaded = 0;
          for (const value of progress.values()) uploaded += value;
          onProgress(Math.round((uploaded / file.size) * 100));
        };

        const uploadWithRetry = async (index: number) => {
          const start = index * CHUNK_SIZE;
          const chunk = file.slice(start, Math.min(start + CHUNK_SIZE, file.size));
          for (let attempt = 0; attempt < MAX_CHUNK_ATTEMPTS; attempt += 1) {
            if (cancelled) throw abortError();
            progress.set(index, 0);
            updateProgress();
            try {
              await uploadChunk(id, index, chunk, (loaded) => {
                progress.set(index, loaded);
                updateProgress();
              });
              progress.set(index, chunk.size);
              updateProgress();
              return;
            } catch (error) {
              // A 400/403/404/413 will answer the same way every time: replaying it
              // only delays the error and re-uploads a payload already refused.
              const retryable =
                error instanceof ChunkRequestError && error.transient;
              if (
                cancelled ||
                !retryable ||
                attempt === MAX_CHUNK_ATTEMPTS - 1
              ) {
                throw error;
              }
              await sleepWhileActive(
                boundedDelay(
                  error instanceof ChunkRequestError
                    ? error.retryAfterMs
                    : undefined,
                  attempt,
                ),
              );
            }
          }
        };

        const worker = async () => {
          while (!cancelled) {
            const index = nextChunk;
            nextChunk += 1;
            if (index >= totalChunks) return;
            await uploadWithRetry(index);
          }
        };
        await Promise.all(
          Array.from({ length: Math.min(WORKER_COUNT, totalChunks) }, () => worker()),
        );
        if (cancelled) throw abortError();

        // Exactly one attempt: the server runs the installer/restorer during merge, so
        // replaying an attempt whose response was lost could install the archive twice.
        let mergeResponse: Response;
        try {
          mergeResponse = await request(
            "upload merge",
            MERGE_TIMEOUT_MS,
            (signal) =>
              fetch(`${basePath}/merge`, {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ upload_id: id }),
                signal,
              }),
            true,
          );
        } catch (error) {
          throw new MergeOutcomeUnknownError(mergeUnknownMessage(error));
        }
        const mergePayload = await readJson<MergeResponse<T>>(mergeResponse);
        if (!mergeResponse.ok || mergePayload.status !== "success") {
          throw new Error(apiMessage(mergePayload, mergeResponse.status));
        }
        uploadID = "";
        onProgress(100);
        return mergePayload.data;
      } catch (error) {
        void cancel().then((outcome) => {
          if (!outcome.cancelled) {
            // The caller only sees `error`, so log the unreleased reservation rather
            // than letting it look like the session was cleaned up.
            console.warn(`[chunkUpload] cancel failed: ${outcome.message}`);
          }
        });
        throw error;
      }
    },
  };
}

function mergeUnknownMessage(error: unknown): string {
  if (error instanceof RequestDeadlineError) {
    return "The server did not answer the merge request in time; the archive may already be installed, so reload before retrying.";
  }
  if (error instanceof DOMException && error.name === "AbortError") {
    // Cancelling during merge does not undo the installation, so this must not be
    // reported as the ordinary "user cancelled, nothing happened" AbortError.
    return "Upload cancelled while the server was installing the archive; it may already be installed, so reload before retrying.";
  }
  return "Lost contact with the server while it was installing the archive; it may already be installed, so reload before retrying.";
}
