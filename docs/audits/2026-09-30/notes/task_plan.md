# Monitoring accuracy repair - 2026-09-30

## Scope and gates
- Preserve existing untracked audit documents; no credentials in artifacts.
- Fix traffic counter/delta semantics and test the deployed SAO repair contract.
- Exclude failed Ping latency, retain all loss trials, merge whole-window statistics.
- Add explicit sampling/quality/epoch boundaries without fabricating legacy data.
- Fix server-detail crash and theme-independent terminal navigation.
- Run regression, module, frontend, browser and deployment checks separately.
- Production requires a complete verified backup and an identified, validated artifact.

## Phases
1. [complete] Established regressions and traced source plus current OC424/MAC-WAN runtime.
2. [complete_local] Traffic semantics, counter continuity and pinned deployed SAO repair contract.
3. [complete_local] Successful Ping latency, independent loss and whole-window statistics.
4. [complete_local] Sampling quality, epoch boundaries, legacy markers and resource definitions.
5. [complete_local] Server-detail and terminal repairs; mounted and shipped-bundle browser checks.
6. [complete] Local tests, vet, race checks, frontend and embedded archive checks passed.
   Native CI/Release supplied the CGO-enabled ARM64 server; both Linux agent builds passed.
7. [complete] Published v1.6.4, upgraded OC424 and MAC-WAN only; numerical canary found a narrow-window defect.
   Published independent v1.6.5 panel hotfix after exact-commit CI and artifact identity checks.
   Full stopped-write backups, deployment, two databases, auth, logs, nginx and host probe passed.
   MAC-WAN kernel/report/query, 5/60/112-second windows, Ping persistence and restart boundary passed.
   Both reported panel clicks and workbench reload passed in the real production browser.
8. [complete] Acceptance report and bounded replay scripts archived in PR #57; all PR CI jobs passed.
   Merged and synchronized main at 01dccaf; final read-only host/fleet identity checks passed.
   Documentation-only main CI 36720071064 passed all five jobs; production remains pinned to v1.6.5/4847bf8.

## Decisions
- No lossy rewriting of historical buckets. Legacy data must remain identifiable.
- Release/tag/push, OC424 deployment and MAC-WAN canary authorized on 2026-09-30.
- No fleet-wide agent upgrade authorized; preserve every other node's current binary.
- Tests asserting old bugs are evidence only, not acceptance tests.
- Historical coarse buckets expose effective coverage; they cannot promise an exact arbitrary raw subwindow.
- Passed rollout scope does not imply CPU numerical, historical-layer or fleet-wide agent acceptance.
- Final evidence is recorded in docs/MONITORING-ACCURACY-FIXES-2026-09-30.md.
