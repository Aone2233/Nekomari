# Emerald Insights v2.0.6 compatibility patch

This is a patch to the existing third-party online theme, **not** a new theme.
The upstream baseline is Chen017/komari-theme-emerald-insights, annotated tag
`v2.0.6`, peeled commit `969a463b5041ce584c87f25ec295cde1af457e47`.
The upstream release archive SHA-256 is
`726cead65fca7375ef2b086ecefbdf0ab7c32e5c20793cc7c9edb874a425849a`.
The production bundle built here is locally modified; it must never be called
an unmodified official release. Its manifest stays at v2.0.6 to preserve the
existing managed settings and theme identity. Reinstalling/updating the theme
from the market replaces this patch, so revalidate after every online update.

The upstream v2.0.6 fixes long-range chart time alignment. This patch additionally:

- Handles nullable `loss` and `p99_p50_ratio`; unknown loss is not displayed as 0%.
- Keeps task ID, actual address family, protocol and role as distinct chart
  identities. Missing labels remain separate rather than guessed; where old
  ungrouped stats cannot be matched safely, do not attach a tagged curve.
- Computes legacy-interface statistics separately from the returned samples;
  escapes task labels inserted into the chart tooltip.
- Labels an empty legacy probe role as the primary target and `reference` as
  a reference point. Only the display label changes: raw series identities
  stay separate, including explicit `primary` versus an empty role. Reference
  cards explain that the point is an auxiliary probe of the same task.
- Hides reference-role detail series by default, including old history.
  `showPingReference` opts in to displaying them without enabling a probe;
  it is a presentation setting and does not mutate tasks or retained samples.
- On dimensioned endpoints, hides retired primary protocol series when the
  current task type unambiguously selects one of ICMP, TCP or HTTP. It leaves
  `dual`, `auto`, unknown types, unknown protocols and explicitly enabled
  references visible. It uses the returned current task metadata and does not
  infer unknown protocols or delete history. Older dimensionless endpoints may
  not provide enough information to apply this distinction.

The official theme's home-card summary uses `common:getRecords`, which removes
the protocol/family/role dimensions and combines records for each task. Its
aggregate latency and loss can therefore mix primary and reference readings;
it is not a reliable per-path measurement. The patched detail chart uses the
dimensioned metric endpoints instead. Do not infer zero loss from missing
samples, and do not merge different address families or protocols.

2026-11-08 earlier compatibility check: the configured MegaBox education task was a
dual ICMP/TCP task with a reference target. Separate IPv6 primary ICMP,
primary TCP and reference ICMP metric series exist. The sanitized fixture in
`fixtures/` covers those shapes, explicit pinned primary roles, independent
selection, desktop/mobile widths and 7/30-day smoothing. This does not expose
the configured target or assert acceptance in the owner's signed-in session.
This was the state before the reference cleanup described below.
The online theme remains upstream v2.0.6 with this local compatibility patch;
an upstream reinstall can overwrite it.

`nekomari.patch` is against the exact upstream commit, including regression
unit tests. The upstream tag ignores its lockfile. `bun.lock` records this
build's independently resolved dependency tree, not upstream release provenance.
Build with Bun 1.3.14 and Node 22.23.4, apply the patch, copy in the lockfile,
run `bun install --frozen-lockfile`, `bun run test` and `bun run build`. Vite's
archive timestamps and build environment can affect byte-for-byte output; use
the recorded bundle hash as an identity for the tested artifact, not a claim
that the official zip is reproducible. Do not install a same-short archive
without a verified backup, a separately retained rollback, and a recheck of the
actual currently active theme and settings.

2026-11-06 local verification: 81 tests pass, type check/build pass, changed
files pass ESLint, and Chromium component fixtures cover nullable stats,
independent multi-series, fallback and 7/30-day smoothing on/off. A production
bundle fixture also runs at desktop and narrow mobile widths. These fixtures are
not signed-in production acceptance and cannot prove the actual node data or
configuration on their own. Built zip SHA-256:
`12d7a267899f390dfa596417ba3d140e0ed4a3ae1acf22086140cfa6b12342e2`.

The clean-source patch application and a fresh Bun frozen-lockfile install/build
were also verified. The original tested zip was additionally opened as a full
mocked instance page in Chromium; the unmodified 2.0.5 and 2.0.6 comparison
components reproduce the null crash and identity collision.

2026-11-08 earlier role-label build: the clean-source patch check and all 81 tests pass.
The actual zip was opened as a mocked full instance page at desktop and mobile
widths, including the primary/reference labels, selection and 7/30-day smoothing.
This supersedes the older bundle hash above. The tested zip SHA-256 is
`8db889bd761bbbb577780632a4974782b76a951c4aa23728fbd643ae6a0f7567`.

2026-11-08 reference-cleanup build: the owner requested removal of unwanted
extra probes, not just a label change. The production task 28 reference was
cleared separately and no reference is configured among the 13 current tasks;
its two primary protocols and past samples are retained. By default this patch
hides reference-role chart stats, latency and loss points, including in the
legacy fallback; the opt-in switch restores them where dimensions exist. A
legacy API that omits roles cannot distinguish historic reference samples.
The home-card aggregate also uses that dimensionless API and may mix roles.
82 tests, production build/typecheck, changed-file lint, and component/full-ZIP
Chromium checks at desktop/mobile widths passed with the switch off and on,
including 7/30-day smoothing. Run the full ZIP check with
`python fixtures/zip-acceptance.browser.spec.py /path/to/theme.zip` in a
Playwright-enabled environment. The tested and deployed ZIP SHA-256 is
`8923beefc031c3cffec7810c99538e439256c15f6e66d35b2fbb88b0af3d43c5`.
It remains a locally patched upstream theme, not an official release; the
owner's signed-in visual acceptance remains outstanding.

2026-11-08 single-protocol follow-up: the owner chose ICMP-only for production
task 28. The patched detail chart filters historical TCP primary samples while
retaining both protocols for `dual`, unknown task types and `auto`. Reference
visibility remains controlled independently. Tests cover missing/conflicting
types, unknown roles/protocols, and references that use a different protocol.
84 theme tests, typecheck/build, changed-file lint, and desktop/mobile component
and full-ZIP Chromium cases passed, including 7/30-day smoothing. This build's
ZIP SHA-256 is `4b6487f0cff286d6ed7b9bffaa39f9c6653ec96dcb21981863717196cec9d918`.
Its public chart/index bytes match the tested build; signed-in visual acceptance
remains the owner's check. See the current deployment checkpoint for the
non-destructive task change, backups, and live Agent observations.
