# Repair findings - 2026-09-30

- Baseline HEAD: a7a8fe7a295e34a04b604d892ecbc0e450293c5d (v1.6.3).
- Existing audit confirms bucket boundary loss, raw counter summation, failed Ping
  latency contamination and averaged bucket percentile/stddev defects.
- Live public theme is SAO 1.0.10; default frontend tests alone are insufficient.
- Authenticated live reproduction confirmed the server-detail Radix slot failure.
  DrawerContent supplied an extra JSX whitespace child; removing it fixes the mounted and shipped bundle.
- Authenticated live reproduction confirmed /terminal was intercepted by the active theme and reached /404.
  The server now routes /terminal and /terminal/ to the admin SPA independently of the public theme.
- Counter deltas now require matching nonempty epochs, monotonic counters/uptime and a bounded sample gap.
  Invalid intervals are unknown, not invented zero-byte samples; retries/out-of-order reports cannot double count.
- Successful Ping latency is stored separately from all-attempt loss. Moments and digests merge before statistics.
- Raw/rollup changes cannot change validated totals for the same covered window. Historical arbitrary
  subwindows disclose backing bucket coverage rather than pretending to retain exact raw boundaries.
- Missing/failed resource collection remains null in latest status; a measured CPU zero remains zero.
  Kernel counters are independent of billing-cycle quality.
- The active deployed SAO asset passed its pinned pure-function delta repair contract. This is not
  authenticated live acceptance against a deployed repaired server.
- Final local tests passed; the initial v1.6.3 production baseline was subsequently upgraded as authorized.
- CGO-disabled Linux server builds are unsupported by the existing release workflow. Linux agents compile;
  formal native CGO-enabled CI/Release and production artifact verification subsequently passed.
- v1.6.4 live canary exposed raw completeness being compared across different bucket coverage. v1.6.5
  checks matching full bucket coverage but returns only requested raw points; point budgets now preserve totals.
- Persisted late insert after restart merges only its delta; parent rollups receive the full merged child snapshot.
- OC424 now runs pinned v1.6.5/4847bf8. MAC-WAN v1.6.4 kernel/report/query and restart-boundary gates passed.
  The other nine agents stay legacy; historical information cannot be fabricated by a server-only upgrade.
