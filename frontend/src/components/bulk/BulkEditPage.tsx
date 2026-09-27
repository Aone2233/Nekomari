import {
  Badge,
  Box,
  Button,
  Card,
  Checkbox,
  Flex,
  Heading,
  Select,
  Table,
  Text,
  TextField,
} from "@radix-ui/themes";
import { useTranslation } from "react-i18next";
import React, { useCallback, useMemo, useState } from "react";
import {
  BILLING_CYCLES,
  TRAFFIC_LIMIT_TYPES,
  buildUpdate,
  emptyForm,
  enabledFieldCount,
  type BulkForm,
  type BulkReport,
} from "@/types/Bulk";

/**
 * Bulk edit (roadmap H2).
 *
 * Two properties decide the whole design, and both come from the acceptance criteria:
 *
 * **Only switched-on fields are sent.** A field has an explicit on/off switch, because the
 * alternative — sending every field with its form value — overwrites each node's unmentioned
 * fields with whatever the form happens to hold. The first bulk edit anyone tries would set a
 * fleet's group to the empty string.
 *
 * **A partial failure is reported per node.** The server applies each update through the same
 * validation a single-node edit uses and returns one outcome per uuid; this page shows all of
 * them, the failures first, because "3 of 10 failed" without saying which three is a report
 * the operator has to re-derive by hand.
 *
 * Takes the node list and the apply callback as props so the mounted fixture can put a
 * partial failure, an empty selection and a mid-poll refresh on screen without a server.
 */

export interface BulkEditPageProps {
  nodes: Array<{ uuid: string; name?: string; group?: string; weight?: number; hidden?: boolean }>;
  /** apply runs the edit. Supplied by the page shell, stubbed by the fixture. */
  apply?: (uuids: string[], update: Record<string, unknown>) => Promise<BulkReport>;
  loading?: boolean;
}

/** A field row: a switch that decides whether this field is part of the update, and its input. */
const Field: React.FC<{
  id: string;
  label: string;
  enabled: boolean;
  onToggle: (enabled: boolean) => void;
  children: React.ReactNode;
}> = ({ id, label, enabled, onToggle, children }) => (
  <Flex align="center" gap="3" data-testid={`bulk-field-${id}`} data-enabled={String(enabled)}>
    <Checkbox
      checked={enabled}
      onCheckedChange={(checked) => onToggle(checked === true)}
      aria-label={label}
      data-testid={`bulk-field-${id}-toggle`}
    />
    <Box width="110px">
      <Text size="2" weight={enabled ? "medium" : "regular"}>
        {label}
      </Text>
    </Box>
    <Box style={{ opacity: enabled ? 1 : 0.5 }}>{children}</Box>
  </Flex>
);

export const BulkEditPage: React.FC<BulkEditPageProps> = ({ nodes, apply, loading = false }) => {
  const { t } = useTranslation();
  const [form, setForm] = useState<BulkForm>(emptyForm);
  const [selected, setSelected] = useState<string[]>([]);
  const [report, setReport] = useState<BulkReport | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [applying, setApplying] = useState(false);

  // Selection is a list of uuids, not of row indices, so a poll that reorders or replaces the
  // node list cannot silently move the selection onto different nodes. The nodes that have
  // since disappeared are dropped on the next render rather than applied to.
  const presentUUIDs = useMemo(() => new Set(nodes.map((node) => node.uuid)), [nodes]);
  const effectiveSelection = useMemo(
    () => selected.filter((uuid) => presentUUIDs.has(uuid)),
    [selected, presentUUIDs],
  );

  const update = useMemo(() => buildUpdate(form), [form]);
  const fieldCount = enabledFieldCount(form);

  const onApply = useCallback(async () => {
    if (!apply || effectiveSelection.length === 0 || fieldCount === 0) return;
    setApplying(true);
    setError(null);
    try {
      setReport(await apply(effectiveSelection, update));
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
      setReport(null);
    } finally {
      setApplying(false);
    }
  }, [apply, effectiveSelection, fieldCount, update]);

  const toggleAll = useCallback(
    (checked: boolean) => setSelected(checked ? nodes.map((node) => node.uuid) : []),
    [nodes],
  );

  const nameOf = useCallback(
    (uuid: string) => nodes.find((node) => node.uuid === uuid)?.name ?? uuid,
    [nodes],
  );

  return (
    <Flex direction="column" gap="4" p="4" data-testid="bulk-page">
      <Flex direction="column" gap="1">
        <Heading size="6">{t("bulk.title")}</Heading>
        <Text size="2" color="gray">
          {t("bulk.subtitle")}
        </Text>
      </Flex>

      <Card data-testid="bulk-fields">
        <Flex direction="column" gap="3">
          <Text size="2" weight="medium">
            {t("bulk.fieldsHeading")}
          </Text>
          <Text size="1" color="gray" data-testid="bulk-fields-hint">
            {t("bulk.fieldsHint")}
          </Text>

          <Field
            id="group"
            label={t("bulk.field.group")}
            enabled={form.group.enabled}
            onToggle={(enabled) => setForm({ ...form, group: { ...form.group, enabled } })}
          >
            <TextField.Root
              value={form.group.value}
              onChange={(event) =>
                setForm({ ...form, group: { ...form.group, value: event.target.value } })
              }
              data-testid="bulk-input-group"
            />
          </Field>

          <Field
            id="tags"
            label={t("bulk.field.tags")}
            enabled={form.tags.enabled}
            onToggle={(enabled) => setForm({ ...form, tags: { ...form.tags, enabled } })}
          >
            <TextField.Root
              value={form.tags.value}
              onChange={(event) =>
                setForm({ ...form, tags: { ...form.tags, value: event.target.value } })
              }
              data-testid="bulk-input-tags"
            />
          </Field>

          <Field
            id="weight"
            label={t("bulk.field.weight")}
            enabled={form.weight.enabled}
            onToggle={(enabled) => setForm({ ...form, weight: { ...form.weight, enabled } })}
          >
            <TextField.Root
              type="number"
              value={String(form.weight.value)}
              onChange={(event) =>
                setForm({ ...form, weight: { ...form.weight, value: Number(event.target.value) } })
              }
              data-testid="bulk-input-weight"
            />
          </Field>

          <Field
            id="hidden"
            label={t("bulk.field.hidden")}
            enabled={form.hidden.enabled}
            onToggle={(enabled) => setForm({ ...form, hidden: { ...form.hidden, enabled } })}
          >
            <Select.Root
              value={form.hidden.value ? "true" : "false"}
              onValueChange={(value) =>
                setForm({ ...form, hidden: { ...form.hidden, value: value === "true" } })
              }
            >
              <Select.Trigger data-testid="bulk-input-hidden" />
              <Select.Content>
                <Select.Item value="false">{t("bulk.hidden.no")}</Select.Item>
                <Select.Item value="true">{t("bulk.hidden.yes")}</Select.Item>
              </Select.Content>
            </Select.Root>
          </Field>

          <Field
            id="price"
            label={t("bulk.field.price")}
            enabled={form.price.enabled}
            onToggle={(enabled) => setForm({ ...form, price: { ...form.price, enabled } })}
          >
            <TextField.Root
              type="number"
              value={String(form.price.value)}
              onChange={(event) =>
                setForm({ ...form, price: { ...form.price, value: Number(event.target.value) } })
              }
              data-testid="bulk-input-price"
            />
          </Field>

          <Field
            id="billingCycle"
            label={t("bulk.field.billingCycle")}
            enabled={form.billingCycle.enabled}
            onToggle={(enabled) =>
              setForm({ ...form, billingCycle: { ...form.billingCycle, enabled } })
            }
          >
            <Select.Root
              value={String(form.billingCycle.value)}
              onValueChange={(value) =>
                setForm({
                  ...form,
                  billingCycle: { ...form.billingCycle, value: Number(value) },
                })
              }
            >
              <Select.Trigger data-testid="bulk-input-billingCycle" />
              <Select.Content>
                {BILLING_CYCLES.map((cycle) => (
                  <Select.Item key={cycle.value} value={String(cycle.value)}>
                    {cycle.label}
                  </Select.Item>
                ))}
              </Select.Content>
            </Select.Root>
          </Field>

          <Field
            id="currency"
            label={t("bulk.field.currency")}
            enabled={form.currency.enabled}
            onToggle={(enabled) => setForm({ ...form, currency: { ...form.currency, enabled } })}
          >
            <TextField.Root
              value={form.currency.value}
              onChange={(event) =>
                setForm({ ...form, currency: { ...form.currency, value: event.target.value } })
              }
              data-testid="bulk-input-currency"
            />
          </Field>

          <Field
            id="trafficLimit"
            label={t("bulk.field.trafficLimit")}
            enabled={form.trafficLimit.enabled}
            onToggle={(enabled) =>
              setForm({ ...form, trafficLimit: { ...form.trafficLimit, enabled } })
            }
          >
            <TextField.Root
              type="number"
              value={String(form.trafficLimit.value)}
              onChange={(event) =>
                setForm({
                  ...form,
                  trafficLimit: { ...form.trafficLimit, value: Number(event.target.value) },
                })
              }
              data-testid="bulk-input-trafficLimit"
            />
          </Field>

          <Field
            id="trafficLimitType"
            label={t("bulk.field.trafficLimitType")}
            enabled={form.trafficLimitType.enabled}
            onToggle={(enabled) =>
              setForm({ ...form, trafficLimitType: { ...form.trafficLimitType, enabled } })
            }
          >
            <Select.Root
              value={form.trafficLimitType.value}
              onValueChange={(value) =>
                setForm({
                  ...form,
                  trafficLimitType: { ...form.trafficLimitType, value },
                })
              }
            >
              <Select.Trigger data-testid="bulk-input-trafficLimitType" />
              <Select.Content>
                {TRAFFIC_LIMIT_TYPES.map((kind) => (
                  <Select.Item key={kind} value={kind}>
                    {kind}
                  </Select.Item>
                ))}
              </Select.Content>
            </Select.Root>
          </Field>
        </Flex>
      </Card>

      <Card data-testid="bulk-nodes">
        <Flex direction="column" gap="3">
          <Flex align="center" justify="between">
            <Text size="2" weight="medium">
              {t("bulk.nodesHeading")}
            </Text>
            <Flex gap="2" align="center">
              <Button
                size="1"
                variant="soft"
                onClick={() => toggleAll(true)}
                data-testid="bulk-select-all"
              >
                {t("bulk.selectAll")}
              </Button>
              <Button
                size="1"
                variant="soft"
                onClick={() => toggleAll(false)}
                data-testid="bulk-select-none"
              >
                {t("bulk.selectNone")}
              </Button>
            </Flex>
          </Flex>

          {loading && nodes.length === 0 ? (
            <Text size="2" color="gray" data-testid="bulk-loading">
              {t("bulk.loading")}
            </Text>
          ) : (
            <Table.Root size="1">
              <Table.Header>
                <Table.Row>
                  <Table.ColumnHeaderCell>{t("bulk.selected")}</Table.ColumnHeaderCell>
                  <Table.ColumnHeaderCell>{t("bulk.node")}</Table.ColumnHeaderCell>
                  <Table.ColumnHeaderCell>{t("bulk.group")}</Table.ColumnHeaderCell>
                  <Table.ColumnHeaderCell>{t("bulk.weight")}</Table.ColumnHeaderCell>
                </Table.Row>
              </Table.Header>
              <Table.Body>
                {nodes.map((node) => {
                  const checked = effectiveSelection.includes(node.uuid);
                  return (
                    <Table.Row
                      key={node.uuid}
                      data-testid="bulk-node-row"
                      data-uuid={node.uuid}
                      data-selected={String(checked)}
                    >
                      <Table.Cell>
                        <Checkbox
                          data-testid="bulk-node-checkbox"
                          checked={checked}
                          onCheckedChange={(value) =>
                            setSelected((current) =>
                              value === true
                                ? [...new Set([...current, node.uuid])]
                                : current.filter((uuid) => uuid !== node.uuid),
                            )
                          }
                          aria-label={node.name ?? node.uuid}
                        />
                      </Table.Cell>
                      <Table.Cell>{node.name ?? node.uuid}</Table.Cell>
                      <Table.Cell>{node.group ?? ""}</Table.Cell>
                      <Table.Cell>{node.weight ?? 0}</Table.Cell>
                    </Table.Row>
                  );
                })}
              </Table.Body>
            </Table.Root>
          )}
        </Flex>
      </Card>

      <Flex align="center" gap="3" wrap="wrap">
        <Button
          onClick={() => void onApply()}
          disabled={!apply || effectiveSelection.length === 0 || fieldCount === 0 || applying}
          loading={applying}
          data-testid="bulk-apply"
        >
          {t("bulk.apply")}
        </Button>
        <Badge data-testid="bulk-selection-count" data-count={effectiveSelection.length}>
          {t("bulk.selectionCount").replace("{{count}}", String(effectiveSelection.length))}
        </Badge>
        <Badge data-testid="bulk-field-count" data-count={fieldCount} color={fieldCount ? "green" : "gray"}>
          {t("bulk.fieldCount").replace("{{count}}", String(fieldCount))}
        </Badge>
        {fieldCount === 0 && (
          <Text size="1" color="gray" data-testid="bulk-fields-off-hint">
            {t("bulk.noFieldsHint")}
          </Text>
        )}
      </Flex>

      {error && (
        <Text size="2" color="red" data-testid="bulk-error">
          {t("bulk.error")}: {error}
        </Text>
      )}

      {report && (
        <Card data-testid="bulk-report" data-applied={report.applied} data-failed={report.failed}>
          <Flex direction="column" gap="2">
            <Text size="2" weight="medium" data-testid="bulk-report-summary">
              {t("bulk.reportSummary")
                .replace("{{applied}}", String(report.applied))
                .replace("{{failed}}", String(report.failed))
                .replace("{{total}}", String(report.total))}
            </Text>
            {report.failed > 0 && (
              <Flex direction="column" gap="1" data-testid="bulk-report-failures">
                <Text size="2" color="red">
                  {t("bulk.failuresHeading")}
                </Text>
                {report.outcomes
                  .filter((outcome) => !outcome.ok)
                  .map((outcome) => (
                    <Text
                      size="1"
                      key={outcome.uuid}
                      data-testid="bulk-failure"
                      data-uuid={outcome.uuid}
                    >
                      {nameOf(outcome.uuid)}: {outcome.error || t("bulk.unknownError")}
                    </Text>
                  ))}
              </Flex>
            )}
            <Text size="1" color="gray" data-testid="bulk-report-fields">
              {t("bulk.fieldsApplied").replace("{{fields}}", report.field_names.join(", "))}
            </Text>
          </Flex>
        </Card>
      )}
    </Flex>
  );
};

export default BulkEditPage;
