# Repair progress - 2026-09-30

## Completed locally
- Read the prior audit, testing and deployment guidance; retained pre-existing untracked work.
- Traced current OC424 data and MAC-WAN collection definitions without production writes.
- Reproduced both panel failures in an authenticated in-app browser.
- Implemented interval traffic, whole-window Ping statistics, sample quality and panel routing repairs.
- Replayed the pinned deployed SAO repair function; delta total 20 and missing data stayed null.
- Root and agent module suites plus vet passed with explicit environment-only denylist skips.
- Race checks passed for metric store, metric engine, RPC, client ingestion and agent connections.
- Frontend: 108 tests, lint and build passed; mounted node table: 8 browser tests passed.
- Final embedded bundle: 4 authenticated local navigation tests and 13-route smoke passed.
- Refreshed embedded admin/standalone assets while preserving 331 theme files byte-for-byte.
- Linux amd64 and arm64 agent cross-builds passed.

## Local-stage limitations and current boundaries
- Chrome connector initially lacked a Codex auth token; authenticated IAB reproduction later succeeded.
- The server requires CGO. CGO-disabled Linux arm64 compilation failed as documented by release.yml.
  This workstation has no available Linux Docker daemon or ARM64 cross-compiler; no toolchain was installed.
- Before rollout authorization, no commit, push, tag or production change was performed.
- OC424/MAC-WAN acceptance is complete within the scope recorded below; other agents remain unchanged.
- Historical data was not rewritten; coarse bucket coverage and legacy unknowns are explicit.
- See docs/MONITORING-ACCURACY-FIXES-2026-09-30.md for evidence and the release/deployment gate.
- The isolated local server was stopped; port 25884 has no listener.
- Cleanup was blocked by execution policy. Own .accuracy-smoke and frontend/.build-tmp directories remain.

## Authorized release rollout - 2026-09-30
- Live baseline at 10:32 UTC: OC424 v1.6.3/a7a8fe7, ARM64, healthy, zero restarts.
- Git main and origin/main agree at a7a8fe7a295e34a04b604d892ecbc0e450293c5d.
- MAC-WAN user service is active; old binary has cap_net_raw=ep and requires sudo authentication.
- Scope: release v1.6.4, upgrade OC424 panel, then MAC-WAN agent only. No other fleet changes.
- PR #55 merged as c83ace28cacccd806cd437ddce2c962ae6daa34d.
- Exact-main CI 36704723022 completed successfully, including both native build platforms.
- Annotated v1.6.4 tag published at that commit; Release and Docker workflows passed and rollout completed.
- Live registered versions before rollout: all ten agents report v1.6.2, including MAC-WAN.
  MAC-WAN's old binary SHA256 is f83ff97493541fe54b476fd7169d563f0be1eb72242a73af5aa2bec78132ad49.
- Read-only acceptance scripts were staged on both hosts; OC424 Python 3.10 timestamp parsing
  required explicit RFC3339 fractional precision normalization, and the fleet baseline then passed.

## Production canary and v1.6.5 hotfix
- v1.6.4 numerical acceptance failed: a recent narrow raw window fell back to a full minute bucket.
  Kept the gate failed and added an independently released v1.6.5 patch and persisted late-insert regressions.
- PR #56 merged/tagged at 4847bf84ba91b7b933e314431fe6d9ec0824a3d7; exact CI, Release and Docker passed.
- Anonymous image pull, architecture/revision, asset checksum and extracted server binary agreed.
- OC424 full stopped-write backup: /opt/nekomari/backups/v1.6.4-before-v1.6.5-20260930T122413Z.
  Pinned v1.6.5 digest deployed; old digest and Compose remain recoverable.
- MAC-WAN kept v1.6.4; no Agent source difference in v1.6.5. Actual binary/capability/service identity verified.
- 23 reports matched kernel brackets; maximum network-rate error 0.118205%; RAM/Swap/disk/connections passed.
- Traffic totals matched exactly for 5/60/112-second windows at point budgets 1/2/500.
- Real agent restart: first new-epoch rate unknown/null, no cross-epoch delta; subsequent sampling recovered.
- Closed two-minute Ping: five groups, exact persisted moments/count/loss match and budget invariance.
- Runtime healthy/zero restarts, both DB quick_check ok, auth 401/200, no fatal/SQLite signatures.
- nginx -t and panel-probe.service Result=success/ExecMainStatus=0 passed; both hosts synchronized clocks.
- v1.6.5 production server-name click, workbench click and reload passed; post-recovery console errors zero.
- Final fleet check: all 10 online, only MAC-WAN v1.6.4; nine unchanged v1.6.2/legacy agents.
- Harness-only corrections: RFC3339Nano fractions normalized to six digits; checked the actual JSON admin
  endpoint rather than the WebSocket route; used panel-probe.service rather than an assumed unit name.
- CPU independent counter comparison, full historical-tier live acceptance and fleet upgrade are not claimed.

## Acceptance archive and final read-only checks
- Seven explicitly selected report/replay files committed as 6dd1a3316ce14c294f3ff8457789e274df31dd4d.
- PR #57 CI 36719153828 passed all five jobs; merged as 01dccaf7b1bf53b54c8c55e4625939c69369a55c.
- Local main fast-forwarded to origin/main; existing untracked work and private evidence remain untouched.
- This documentation-only merge did not create a release, change the v1.6.5 tag or restart production.
- 21:17 Asia/Shanghai: final OC424 identity, health, two DB quick_check, auth 401/200 and logs passed.
- Final fleet: 10/10 online, maximum report age 4.424 seconds; versions remain nine v1.6.2 and MAC-WAN v1.6.4.
- 21:18:52 Asia/Shanghai: MAC binary SHA256 and cap_net_raw matched; service active/running, zero restarts.
- Final evidence saved separately as runtime-final-result.json, fleet-final-result.json and mac-identity-final.txt.
- Main archive CI 36720071064 completed successfully at exact merge SHA 01dccaf; all five jobs passed.
- Release, bounded production deployment, new-agent acceptance and evidence archival are complete.
