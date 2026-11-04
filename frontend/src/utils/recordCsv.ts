/** Build a bounded bucket export without converting an explicit range to "now". */
export function recordCsvUrl(
  kind: "load" | "ping",
  uuid: string,
  range: { start: string; end: string } | { hours: number },
): string {
  const params = new URLSearchParams({ uuid });
  if ("start" in range) {
    params.set("start", range.start);
    params.set("end", range.end);
  } else {
    params.set("hours", String(Math.max(1, Math.min(Math.ceil(range.hours), 8784))));
  }
  return `/api/records/${kind}.csv?${params}`;
}

const maxDownloadBytes = 32 * 1024 * 1024;

/** Download without navigating to a JSON error page or buffering without a cap. */
export async function downloadRecordCsv(url: string, filename: string): Promise<void> {
  const controller = new AbortController();
  const timer = window.setTimeout(() => controller.abort(), 60_000);
  try {
    const response = await fetch(url, {
      credentials: "same-origin",
      cache: "no-store",
      headers: { Accept: "text/csv" },
      signal: controller.signal,
    });
    if (response.status === 413) throw new Error("csv_too_large");
    if (!response.ok || !response.headers.get("content-type")?.toLowerCase().startsWith("text/csv") || !response.body) {
      throw new Error("csv_download_failed");
    }
    const reader = response.body.getReader();
    const parts: BlobPart[] = [];
    let size = 0;
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > maxDownloadBytes) {
        await reader.cancel();
        throw new Error("csv_too_large");
      }
      parts.push(value);
    }
    const objectURL = URL.createObjectURL(new Blob(parts, { type: "text/csv;charset=utf-8" }));
    try {
      const link = document.createElement("a");
      link.href = objectURL;
      link.download = filename;
      document.body.appendChild(link);
      link.click();
      link.remove();
    } finally {
      window.setTimeout(() => URL.revokeObjectURL(objectURL), 0);
    }
  } finally {
    window.clearTimeout(timer);
  }
}
