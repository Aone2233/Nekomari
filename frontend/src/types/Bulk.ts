/**
 * Types and helpers for the bulk edit page (roadmap H2).
 *
 * The rule this file exists to keep: **a bulk edit sends only the fields the operator
 * switched on.** Sending every field with its current value would overwrite each node's
 * unmentioned fields with the form's defaults — the fastest way to set a whole fleet's group
 * to the empty string, and the reason the envelope is `{enabled, value}` per field rather
 * than a flat object.
 */

/** BulkField is one editable client field, either off (not sent) or on with a value. */
export interface BulkField<T> {
  enabled: boolean;
  value: T;
}

/** makeField gives a field its off state. */
export function makeField<T>(value: T): BulkField<T> {
  return { enabled: false, value };
}

/** The fields the page can edit. Deliberately administrative ones: name, addresses and
 *  hardware facts come from the node's own reports and must not be overwritten by hand. */
export interface BulkForm {
  group: BulkField<string>;
  tags: BulkField<string>;
  weight: BulkField<number>;
  hidden: BulkField<boolean>;
  price: BulkField<number>;
  billingCycle: BulkField<number>;
  currency: BulkField<string>;
  trafficLimit: BulkField<number>;
  trafficLimitType: BulkField<string>;
}

/** The JSON field names each form field writes, and what the update envelope looks like. */
const FIELD_KEYS: Record<keyof BulkForm, string> = {
  group: "group",
  tags: "tags",
  weight: "weight",
  hidden: "hidden",
  price: "price",
  billingCycle: "billing_cycle",
  currency: "currency",
  trafficLimit: "traffic_limit",
  trafficLimitType: "traffic_limit_type",
};

export const emptyForm = (): BulkForm => ({
  group: makeField(""),
  tags: makeField(""),
  weight: makeField(0),
  hidden: makeField(false),
  price: makeField(0),
  billingCycle: makeField(1),
  currency: makeField("$"),
  trafficLimit: makeField(0),
  trafficLimitType: makeField("max"),
});

/**
 * buildUpdate turns the form into the RPC's update object.
 *
 * Only enabled fields are included — see the note above. An empty result is returned as an
 * empty object rather than throwing, so the caller can disable its apply button instead of
 * discovering the problem from an error.
 */
export function buildUpdate(form: BulkForm): Record<string, unknown> {
  const update: Record<string, unknown> = {};
  for (const key of Object.keys(FIELD_KEYS) as Array<keyof BulkForm>) {
    const field = form[key];
    if (!field.enabled) continue;
    update[FIELD_KEYS[key]] = field.value;
  }
  return update;
}

/** enabledFieldCount is how many fields an apply would send. */
export function enabledFieldCount(form: BulkForm): number {
  return Object.values(form).filter((field) => field.enabled).length;
}

/** The billing cycles the panel offers, in months; 0 means one-off. */
export const BILLING_CYCLES: Array<{ value: number; label: string }> = [
  { value: 0, label: "one-off" },
  { value: 1, label: "monthly" },
  { value: 3, label: "quarterly" },
  { value: 6, label: "half-yearly" },
  { value: 12, label: "yearly" },
  { value: 24, label: "biennial" },
  { value: 36, label: "triennial" },
];

/** The traffic-limit kinds the panel supports. */
export const TRAFFIC_LIMIT_TYPES = ["max", "min", "sum", "up", "down"];

/** An outcome as the server reports it. */
export interface BulkOutcome {
  uuid: string;
  ok: boolean;
  error?: string;
}

/** The bulk edit result as the server reports it. */
export interface BulkReport {
  applied: number;
  failed: number;
  total: number;
  outcomes: BulkOutcome[];
  field_names: string[];
}
