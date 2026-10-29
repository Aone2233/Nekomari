# Default-theme cache history

Published as v1.7.14 and deployed on OC424 on 2026-10-29. This adds two
independently retained observations to the session-only Performance profiles
history. This is one bounded v1.8 observability increment, not an SLO, a
continuous disk check or a complete v1.8 release. The published binary passed
isolated MAC-WAN browser checks; both series received natural OC424 samples,
and a post-upgrade encrypted backup passed retention-aware recovery. See
[`DEPLOY-OC424.md`](./DEPLOY-OC424.md) for the exact evidence and boundaries.

The existing approximately one-minute sampler reads the non-invasive
default-theme process snapshot, including on startup. The two metrics capture
the last observed disk state and whether the in-memory fallback has loaded in
that process. They do not cause archive extraction, scan the disk, monitor
other themes, or alter cache policy. See
[`THEME-CACHE-DIAGNOSTICS.md`](./THEME-CACHE-DIAGNOSTICS.md) for the states'
meaning and the limitations of the last-known `ready` state.

Each bucket contains only its last observation. State changes between
samples, or earlier in the same coarse bucket, can be missed; bucket labels are
not exact transition times. The UI uses unconnected points, including when
there are many isolated readings, so it does not imply a measured transition
or bridge unknown gaps. Its last-observed bucket may be older than the end of
the window. An unknown future disk state is stored as an unknown sentinel and
returned as `null`; absent fields from older servers are also unknown, not
zero. The memory flag is independently sampled, and resets on restart.

Both definitions default to one day, including during legacy migration. The
existing independent retention control may disable and remove either series
without disabling the other. Normal close persists stored samples; unclean
stops can lose recent in-memory values. Store reload, sampling failures and
missing observations may leave gaps. The existing fixed windows, shared
10,000-row/8 MiB read budget, deadline and store-operation lease apply. The
panel definitions and endpoint remain private to administrator sessions; no
API key, Agent, schema or external endpoint is added.

## Rollback

Preserve a fresh verified encrypted post-upgrade backup before downgrade or
manual reclaim. v1.7.13 and earlier do not recognize these definitions;
their manual metric-store reclaim deletes the new history even if an
image-only rollback otherwise keeps the current database. Never replace the
current 13-node production data with an older 14-node backup. The v1.7 to
v1.6 authentication migration has a separate paired-binary/database boundary.
Independent-media recovery, actual notification delivery, owner UI acceptance
and full v1.8 remain separate, unverified gates.
