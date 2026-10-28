# Node uptime history

Status: v1.7.8 candidate, not yet published or deployed. This is one bounded
part of v1.8 observability, not reboot counting or v1.8 completion.

## Meaning and use

The built-in `system.uptime_seconds` records uptime already reported by an
Agent; no Agent update or wire-format change is needed. A positive reading with
`quality.uptime=ok`, or a positive legacy reading without a quality field, is
accepted. A non-`ok` quality, a negative value, zero or a missing field is not
recorded. The existing wire decoder cannot distinguish an absent field from a
real zero, even when a report claims quality `ok`, so zero is conservatively
unknown rather than an invented measurement. History begins only after this
version starts recording; old values are not reconstructed.

The admin Operations menu opens `Node uptime history` inside the panel's own
`/admin` shell, including when a third-party public theme is installed. Choose
one node, a 1-hour, 24-hour or 7-day window, and refresh on demand. The default
theme also offers uptime as an optional node chart metric. The endpoint uses the
existing node visibility and scoped `nodes:read` rules and existing bounded
metric query budgets; it returns no hidden nodes to guests or unrelated scopes.

Historical queries default to the **last** accepted sample in each time bucket.
Missing intervals are unknown, not zero. A drop does not prove a reboot; a
coarse bucket can also hide a drop entirely, so neither the curve nor a count of
drops is a reboot count. Bucket labels identify the bucket, not the exact sample
time. No alert, reboot inference, SLO or availability calculation is added.

The new definition defaults to one day of retention on normal startup; existing
metric policies are left unchanged. Administrators can set this metric's own
retention, including zero to disable it, through the existing metric settings.
The view displays the current policy; choosing seven days does not extend it.
Cleanup and system-record deletion include this metric.

## Validation and rollback boundary

Source tests cover quality/legacy inputs, retention and deletion, default last
aggregation, scoped/hidden-node visibility, and restart durability. The
installed-binary browser check uses synthetic loopback accounts and nodes,
including hidden and empty nodes, desktop-light and mobile-dark. Production
acceptance and published-artifact results must be documented separately.
The local candidate -> published v1.7.7 -> local candidate synthetic binary
restart preserves a recorded value without reclaim; it does not replace a
post-upgrade production-backup restore drill.

The metric uses existing tables, but **older images do not recognize its
definition**. Rolling back to v1.7.7 can keep the current database and existing
nodes, but manually reclaiming metric storage on the old version deletes
unknown definitions and their uptime history. Before rollback or reclaim, keep a
fresh encrypted post-upgrade backup and verify an isolated restore. Do not
restore an older backup over newer nodes; record any data-loss window if a
database restore is actually required. A successful pre-upgrade restore alone
does not demonstrate compatibility with post-upgrade uptime data.
