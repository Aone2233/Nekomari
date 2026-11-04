# Metric CSV exports

The fixes described here are in development, not yet released or deployed.
The published v1.7.28 record CSV schema differs; do not treat that version's
reconstructed records as raw observations or use them for traffic billing.

## History bucket endpoints

`GET /api/records/load.csv` and `GET /api/records/ping.csv` return one row per
retained metric series and time bucket, encoded as UTF-8 with BOM and CRLF.
They are aggregates from the metric store, not individual Agent or Ping
reports. A missing sample is absent, never fabricated as a zero. Each row has:

`bucket_start_utc,bucket_interval_seconds,metric,aggregation,client,task_id,ping_type,family,role,value,sample_count,tags_json`

The interval and sample count describe the aggregate's time bucket, not proof
that the node reported throughout it. `tags_json` preserves all stored series
tags; the dedicated ping columns duplicate common tags for convenience. Metrics
with different retention periods can have gaps. Empty data produces a header
only. A UTF-8 BOM and spreadsheet-safe text prefixes should be handled by
machine consumers; numeric metric values, including negative Ping loss and
unknown backup age, remain numeric.

CPU, memory, disk, load, throughput and process metrics use `avg`. Kernel
network totals, cycle traffic counters, backup age/status and Ping latency use
`last`; Ping loss may be `-1` and intermediate Ping events are not preserved.
`traffic.up` and `traffic.down` are cycle-to-date counters: do not sum them
across samples. Historical cycle-counter quality may be unknown. No export
establishes provider billing equivalence. Totals and temperature are not
invented when the underlying store lacks those metrics.

Pass either integer `hours` (default 4, maximum 8784), or both `start` and
`end` as RFC3339 timestamps including time zones. The range is half-open
`[start,end)`, positive and at most 366 days. As historic rollups cannot be
split, only buckets wholly within the requested range are exported. Partial
edge buckets are excluded even if they contain samples, and a range shorter
than the applicable bucket interval can therefore be empty. The response
headers `X-Nekomari-Window-Start`, `X-Nekomari-Window-End-Exclusive` and
`X-Nekomari-Window-Semantics` report the requested bounds; these headers are
not embedded in the file, so save the selection separately if needed.

Load exports require `uuid` except for callers permitted to read private
nodes; Ping exports require `uuid`, `task_id`, or both. Hidden and deleted
nodes follow the same visibility checks as historical JSON. Private-site
middleware applies. `max_rows` defaults to 100000, up to 250000, and overflow
returns 413 before a CSV attachment starts. The existing query/read budget may
reject a large request sooner; narrow the window in that case. Query results
are materialized before the limit is checked: the endpoint is bounded, but is
not a constant-memory, end-to-end streaming query. The panel downloads at most
32 MiB per file and reports errors without navigating away; use a shorter
range if that client-side limit is reached. API clients do not have that
browser-specific size limit.

## Chart export

The chart's wide CSV uses the already loaded chart series, which may have been
aggregated or downsampled by the API. Series headers are CSV-escaped and
disambiguated; they are not a guarantee of original raw samples. The history
bucket button is a separate export and can return a different time resolution.

## Compatibility

The history endpoint columns deliberately replace the newly introduced
v1.7.28 reconstructed wide-load and Ping-record schemas. Consumers written
against that released schema must migrate to the long format above. The
existing JSON endpoints are not changed by this patch. Neither the server
schema nor the UI labels imply that absent data or partial time windows are
measurements of zero.
