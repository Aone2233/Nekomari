# Cost forecast: design and decision gate

Status: proposal only, 2026-11-05. No billing model has been accepted, no
cost forecast is implemented, and neither the active theme nor production
settings are changed. This design is independent of the unreleased execution
history PR. Any subsequent operator-visible release remains gated by owner
acceptance of both deployed CSV exports and the usual release and recovery
checks.

## What the panel can and cannot know

- A node's `price`, `currency`, `billing_cycle`, `expired_at`, and
  `auto_renewal` are operator-supplied service metadata. `price == -1` is
  shown as free, `price == 0` is not displayed, and a billing cycle of `-1`
  denotes a one-time payment. These fields are not a provider invoice,
  overage rate, proof of a payment, or an exact forecast of renewal.
- `utils/renewal/renewal.go` updates an expiry for online nodes with auto
  renewal enabled, using calendar presets and local-time rules. That panel
  operation does not establish that the provider charged or renewed a plan.
  The dashboard has a separate renewal calculation; do not build another
  subtly different date algorithm or mutate expiry while forecasting.
- `admin:getTrafficForecast` projects traffic over a *panel-wide monthly
  traffic reset window*, not necessarily the node's service billing cycle.
  It returns its sample basis, limit type and refusal state. Directions and
  cycle boundaries have the limitations in `FORECAST-DIRECTIONS.md`. A
  provider can count a different interface, direction, unit, timezone or
  window; panel data alone cannot establish billing equivalence.
- SAO's `costPremiums` is theme-specific configuration, keyed by theme short
  name; the live entries were copied between themes on owner action as
  recorded in `SAO-CYCLE-ROLLOUT.md`. It is neither a stable panel tariff
  schema nor necessarily a traffic-overage rate. Do not read, sync, overwrite
  or reinterpret it automatically; changing themes must not change a financial
  calculation without an explicit, reviewed import and owner confirmation.

In particular, dividing `price` by `traffic_limit`, multiplying price by
projected traffic, treating an unknown overage rate as zero, or summing unlike
currency strings would invent a bill. The audit already makes the billing
model a product decision (`audits/2026-10-18/feature-gaps.md`, FG-15).

## Proposed first model for review

If the owner wants traffic-based estimates, start with an **explicit, opt-in,
per-node flat allowance plus linear overage** model, without assuming that it
fits every provider. A node without a confirmed model reports `not configured`,
not zero. Unsupported tariffs (tiered rates, minimum fees, taxes, daily caps,
pooled accounts, throttling instead of charges, etc.) remain unsupported until
their semantics are deliberately added. Proposed inputs must include:

1. The selected traffic direction (`sum`, `up`, `down`, `max`, or `min`) and
   allowance in an explicitly named byte unit; the operator confirms they
   correspond to the provider's rule rather than the UI inferring that fact.
2. A nonnegative overage rate with an explicit decimal unit (for example,
   currency per GB of 1,000,000,000 bytes, or per GiB of 1,073,741,824
   bytes), provider rounding increment, and rounding direction. A numeric
   zero is permitted only when the operator explicitly entered zero.
3. A specific traffic billing window and timezone, including reset day and
   start/end boundaries. Only use a panel projection whose window and direction
   match that configured tariff; otherwise report unavailable. Do not adjust
   the agent ledger, global reset day, or provider configuration.
4. An explicitly identified currency and, if a fixed recurring amount is
   included, its amount and interval. No automatic foreign-exchange conversion,
   aggregation across currencies, or inference that a recurring service
   price is the current traffic-window base fee. Display the base and
   projected overage separately; total only when their period and currency
   are explicitly compatible. Refunds, discounts and taxes stay excluded
   unless they are modeled and labelled.

Illustrative arithmetic only: with an allowance of 1,000 GB, projected usage
of 1,200 GB and an explicitly configured rate of 0.02 currency units per
decimal GB, the unrounded overage estimate is `(1,200 - 1,000) * 0.02 = 4`.
This is not a provider quote; changing to GiB, rounding each billing block, a
different direction, or a different cycle changes the answer. Use integer
minor units or bounded decimal/rational arithmetic, not binary float for
financial rounding; reject overflow and malformed/non-finite values. Currency
minor-unit assumptions must be explicit rather than assuming every currency
uses two decimal places.

## Presentation, access and data lifecycle

- Clearly label every amount **estimate**, with traffic window, source,
  direction, allowance, rate, unit, rounding, currency and forecast basis.
  Show a separate observed-usage calculation only when the usage is known;
  an insufficient or refused projection is unavailable, not a zero amount.
  A provider's actual charge, tax, discount and exchange rate remain unknown.
- Review access before choosing an endpoint. Existing `nodes:read` keys can
  call `admin:getTrafficForecast` and read node metadata; the new tariff may
  contain more sensitive contract information. Do not silently widen existing
  key access or expose hidden-node or contract details to guests through the
  public/theme API. An admin-session-only method is a conservative initial
  candidate, not a final permission decision.
- Define tariff ownership independently of themes, preserving existing
  `price` fields and API behavior. New fields/table, bounded input, audit
  logging without secrets, deletion policy, configuration export/import,
  backup/restore, schema migration and old-image rollback all need a reviewed
  contract before implementation. A future optional theme-data import must
  be a previewable, explicit mapping, never an automatic overwrite.
- Do not turn an estimate into a notification, provider action, node command,
  renewal, or ledger reset. Such automation needs separate approval and
  failure/duplicate-delivery semantics.

## Required decisions and acceptance

The owner must first decide whether the flat-allowance/linear-overage model is
wanted and provide a **sanitized example** of the provider's traffic unit,
direction, reset timezone/window and rounding rules. The owner need not share
API keys, account identifiers, full invoices or actual prices. If the desired
feature is only recurring subscription-cost planning, define that separately;
do not call it traffic-overage forecasting.

After the decision, implement the chosen model in a separate bounded slice.
Acceptance must include known arithmetic examples and boundaries (exact
allowance, rounding blocks, large values, zero versus unknown, invalid input,
mixed currencies); traffic-refusal/window mismatch and timezone/DST cases;
guest/unrelated-key denial and hidden-node handling; import/export and
upgrade/rollback preservation; five-locale responsive UI; and a synthetic
real-panel test. Owner comparison against a redacted provider example is a
separate check and cannot prove all providers' billing rules. No production
traffic histories, node tokens or theme configurations are modified by the
design.
