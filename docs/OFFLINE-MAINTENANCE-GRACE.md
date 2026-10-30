# Offline notifications: maintenance and grace

This is a bounded correctness change, not the v1.8 alert-test button or a
claim that actual notifications reached an operator.

An offline alert waits for both the configured grace period and any active
maintenance window. When the window ends first, the original outage stays
queued until the grace period also expires. When the window begins during the
grace period, the expiry rechecks maintenance and defers until its end. The
existing sweeper rechecks any subsequently overlapping window. A reconnect
before the alert cancels the queued outage without a recovery notice; a later
disconnect starts a fresh outage and grace period. Queue removal is guarded
against a stale sweep deleting a newer connection's outage.

No historical data, schema, API, Agent, notification provider or rule settings
change. The deferred queue remains in memory and can be lost on Panel restart;
it is processed by the existing minute sweeper, with the existing 30-minute
stale-deferral limit and transport retry behavior. These are not delivery or
latency guarantees. Ordinary notification/provider failure behavior remains
unchanged. Window edits still use the existing cached-window behavior.

Focused tests exercise a real SQLite-backed notifier with an in-process message
recorder, real grace timers, reconnects and maintenance decisions, plus a
controlled-clock stale-queue race. `deploy/offline-maintenance-smoke.py` also
boots a disposable Panel, connects synthetic Agents and receives notifications
at a loopback-only fake Telegram endpoint. These do not inject failures into
production or prove real remote message receipt.
