# SAO cycle-traffic rollout, 2026-11-02

This is a dated checkpoint, not the live open-work list. See
[`OPEN-WORK.md`](./OPEN-WORK.md) for current status.

The owner confirms that the upstream SAO v1.3.4 installed on OC424 is their
upgrade. Its 322 files match upstream's v1.3.4 release archive, SHA-256
`44b7e226c02ba411863493799c772e02113cb9d5216902846f5e2cad1f157af1`.
The active panel remains Nekomari v1.7.20/`065bb4d`; no new panel or Agent
release, database change, node change or theme activation is part of this
checkpoint.

## Local variant and verification

The independently named `SAO-Nekomari` v1.3.4-nekomari.1 is an unofficial
[release](https://github.com/Aone2233/Komari-Theme-SAO/releases/tag/v1.3.4-nekomari.1)
based on upstream `918fb899bb3a8b05fc9a555628841ccc35d372a2` and local
commit `ed6dd31ae926d31f4a3cfa4561fc8a94501f425e`. Its published ZIP was
downloaded and checked against SHA-256
`9bdacc816dcc195b2592dc9f4eaf663d1f2444f9cd3ebe8c2e44b80f1084f90b`.
The build is local; no remote build provenance or CI is asserted.

Quality-marked Agent cycle counters replace boot totals in quota and instance
cycle fields; mobile mini cards label the cycle explicitly. Valid zero remains
zero, and missing, failed or invalid cycle samples remain unknown. Desktop and
mobile browser checks against the released v1.7.20 panel and synthetic Agent
WebSocket passed for normal, zero, unknown, recovered and offline data. The
389 unit tests, typecheck and lint pass, including initial-load and store-restart
cases. A separate synthetic panel cutover copied settings, retained all eight
old assets through `/assets/`, and rolled back. These are synthetic checks,
not owner-session or provider-bill acceptance.

**Today traffic (fixed in v1.3.4-nekomari.2):** the independent variant now
validates series semantics before summing. A direction only counts when the
backend answers exactly one `interval_delta_v2` stream with complete,
non-negative integer per-interval points; `billing_cycle_cumulative`
fallbacks, duplicates and partial streams render as unknown, and boundary
repair never merges cumulative counters into validated buckets. The display
is a sampled, possibly-incomplete interval sum, not a provider bill. Agent
reset day, billing directions and time zone may differ from a provider; no
Agent rotation day or ledger has been changed.

## OC424 staging and recovery

The candidate is staged in a new `SAO-Nekomari` directory; upstream `SAO` stays
active and intact. The candidate contains its 322 release files plus eight
old, content-hashed assets from the existing theme, without path collisions.
This preserves those assets for existing pages when the active theme changes;
it is not a general guarantee for other historic cached releases.

Before staging, a fresh encrypted backup was received on MAC-WAN at
`/home/macos/.local/share/nekomari-recovery/manual/sao-v134-cycle-pre-8ce0ff8e3c`:
`oc424-20261102T123802675979Z-711c8e51d64a.tar.gz.age`, SHA-256
`ba92887ffd7a559380014025e40f90cad47edca5025757a40b4285eaca576f3d`.
Decryption and archive hashes, both SQLite checks, all 14 current node
identities and the current 322-file SAO manifest were checked in temporary
memory on MAC-WAN. This verifies the archive and selected contents; it is not
an independent-media restore drill or a guarantee of cross-database atomicity.
The existing scheduled retention and timers were not changed.

An authenticated administrator session is required for theme settings and
activation; do not read or forge another person's session or write the settings
database directly. Immediately before activation, recheck container health,
active upstream theme and its manifest, the staged candidate and old assets,
the current node identity, recent backup health and the encrypted backup.
Stop if unexpected changes appear. Copy the current `SAO` settings to
`SAO-Nekomari` using the authenticated API and verify them before switching.
Settings are keyed by theme short name and will then diverge: before rollback,
preserve any subsequent local settings and explicitly copy them back if needed.
Select upstream `SAO` to roll back; do not restore an older database, node list,
or Agent configuration. Verify the public page, panel health and node identity
after either switch and request owner-session acceptance.

## Activation and retro settings sync (2026-11-02 evening)

The owner activated the staged `SAO-Nekomari` (`v1.3.4-nekomari.2`, archive
SHA-256 `ed270123dd5498b4e1bf3245cf500b20d5f2f210ac5815c7ed0e69b431500f66`)
through the authenticated `/admin/themes` UI on 2026-11-02. Panel continuity
is unaffected: the container stays healthy/restart0 on v1.7.21/`8ea6375`.

The pre-activation settings copy was not performed, so the new theme's
per-short-name configuration started from staging defaults. The visible gap is
cost premium data: `SAO` carries six `costPremiums` entries while
`SAO-Nekomari` has an empty `costPremiums` object. Three of the six entries
reference nodes that no longer exist in the fleet after the owner's confirmed
node changes (`3ce4da47-a296-44ed-9f68-3f1f9542a061`,
`4d4cc4ab-0f76-4e25-8b3e-41bf7e44737b`,
`b46a4f67-71c1-43e7-a515-3d4adb31ecf9`); their entries remain intact in the
`SAO` configuration. The three live entries were prepared as a merged payload
and provided to the owner as an authenticated console `fetch` against
`/api/admin/theme/settings?theme=SAO-Nekomari`; the settings database is not
written directly. Verification after the owner applies the payload: the row
for `SAO-Nekomari` carries the three live entries and `/api/public`
`theme_settings.costPremiums` matches, without a panel restart.
