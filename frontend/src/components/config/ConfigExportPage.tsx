import { Badge, Button, Card, Checkbox, Flex, Heading, Table, Text, TextArea } from "@radix-ui/themes";
import { useTranslation } from "react-i18next";
import React, { useCallback, useMemo, useState } from "react";
import { parseDocument, planCounts } from "./configFormat";

/**
 * Configuration export and import (roadmap H4).
 *
 * Takes the three operations as props, so the mounted fixture can put a refusal, a plan with
 * removals and a successful import on screen without a server.
 *
 * The design follows one rule: **the operator sees the plan before anything is written.** An import
 * is the most consequential thing this panel can do — it rewrites nodes, tasks, windows and settings
 * at once — and a page that offered only "Import" would be asking for trust in a file. So the paste
 * box is followed by a dry run, and Import is disabled until a dry run has been shown for the
 * document currently in the box.
 *
 * Three smaller decisions, each of which a careless version gets wrong:
 *
 *   - **The dry run is invalidated when the text changes.** A plan for a document that is no longer
 *     in the box is worse than no plan, because it looks like one.
 *   - **Removals are shown as "will not be deleted", not as a warning to act on.** The server never
 *     deletes on import, and a page that implied otherwise would make an operator edit their file in
 *     fear.
 *   - **"Include secrets" says what it does.** The box produces a file that carries agent tokens, and
 *     the label says so rather than being a quiet checkbox next to Export.
 */

export interface ImportPlanChange {
  kind: "create" | "update" | "unchanged" | "remove";
  entity: string;
  id: string;
  name?: string;
  fields?: string[];
}

export interface ImportPlan {
  schema_version: number;
  creates: number;
  updates: number;
  unchanged: number;
  removals: number;
  changes: ImportPlanChange[] | null;
  warnings?: string[] | null;
}

export interface ConfigExportPageProps {
  /** exportConfig returns the document the page offers as a file. */
  exportConfig?: (includeSecrets: boolean) => Promise<Record<string, unknown>>;
  /** planImport reports what an import would do, without doing it. */
  planImport?: (document: Record<string, unknown>) => Promise<ImportPlan>;
  /** importConfig applies the document. */
  importConfig?: (document: Record<string, unknown>) => Promise<ImportPlan>;
  /** initialText lets the fixture start with a document already pasted. */
  initialText?: string;
}

export const ConfigExportPage: React.FC<ConfigExportPageProps> = ({
  exportConfig,
  planImport,
  importConfig,
  initialText = "",
}) => {
  const { t } = useTranslation();
  const [includeSecrets, setIncludeSecrets] = useState(false);
  const [text, setText] = useState(initialText);
  // The text the plan below was computed for. Storing it rather than a boolean is what invalidates
  // the plan when the box changes: a plan for a different document is worse than no plan.
  const [plannedFor, setPlannedFor] = useState<string | null>(null);
  const [plan, setPlan] = useState<ImportPlan | null>(null);
  const [result, setResult] = useState<ImportPlan | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const parsed = useMemo(() => parseDocument(text), [text]);
  const planIsCurrent = plan !== null && plannedFor === text;

  const onExport = useCallback(async () => {
    if (!exportConfig) return;
    setBusy(true);
    setError(null);
    try {
      const document = await exportConfig(includeSecrets);
      const encoded = JSON.stringify(document, null, 2);
      setText(encoded);
      setPlan(null);
      setPlannedFor(null);
      setResult(null);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setBusy(false);
    }
  }, [exportConfig, includeSecrets]);

  const onPlan = useCallback(async () => {
    if (!planImport || !parsed.document) return;
    setBusy(true);
    setError(null);
    setResult(null);
    try {
      const computed = await planImport(parsed.document);
      setPlan(computed);
      setPlannedFor(text);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
      setPlan(null);
      setPlannedFor(null);
    } finally {
      setBusy(false);
    }
  }, [planImport, parsed.document, text]);

  const onImport = useCallback(async () => {
    if (!importConfig || !parsed.document || !planIsCurrent) return;
    setBusy(true);
    setError(null);
    try {
      setResult(await importConfig(parsed.document));
      // The plan is stale now: the panel has changed under it.
      setPlan(null);
      setPlannedFor(null);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setBusy(false);
    }
  }, [importConfig, parsed.document, planIsCurrent]);

  const onChangeText = useCallback((value: string) => {
    setText(value);
    // Anything typed after a plan invalidates it.
    if (value !== plannedFor) {
      setPlan(null);
    }
  }, [plannedFor]);

  return (
    <Flex direction="column" gap="4" p="4" data-testid="config-page">
      <Flex direction="column" gap="1">
        <Heading size="6">{t("config.title")}</Heading>
        <Text size="2" color="gray">
          {t("config.subtitle")}
        </Text>
      </Flex>

      <Card data-testid="config-export">
        <Flex align="center" gap="3" wrap="wrap">
          <Button onClick={() => void onExport()} disabled={!exportConfig || busy}
                  data-testid="config-export-button">
            {t("config.export")}
          </Button>
          <Flex align="center" gap="2">
            <Checkbox
              checked={includeSecrets}
              onCheckedChange={(checked) => setIncludeSecrets(checked === true)}
              data-testid="config-include-secrets"
            />
            <Text size="2" weight={includeSecrets ? "medium" : "regular"}
                  color={includeSecrets ? "red" : undefined}>
              {t("config.includeSecrets")}
            </Text>
          </Flex>
          {includeSecrets && (
            <Badge color="red" data-testid="config-secrets-warning">
              {t("config.secretsWarning")}
            </Badge>
          )}
        </Flex>
      </Card>

      <Card data-testid="config-document">
        <Flex direction="column" gap="3">
          <Text size="2" weight="medium">
            {t("config.documentHeading")}
          </Text>
          <TextArea
            value={text}
            onChange={(event) => onChangeText(event.target.value)}
            rows={10}
            placeholder={t("config.documentPlaceholder")}
            data-testid="config-document-text"
          />
          <Flex align="center" gap="3" wrap="wrap">
            <Button size="2" variant="soft" onClick={() => void onPlan()}
                    disabled={!planImport || !parsed.document || busy}
                    data-testid="config-plan-button">
              {t("config.dryRun")}
            </Button>
            <Button size="2" onClick={() => void onImport()}
                    disabled={!importConfig || !planIsCurrent || busy}
                    data-testid="config-import-button">
              {t("config.import")}
            </Button>
            {parsed.error && (
              <Text size="1" color="red" data-testid="config-parse-error">
                {t("config.parseError")}: {parsed.error}
              </Text>
            )}
            {!planIsCurrent && parsed.document && (
              <Text size="1" color="gray" data-testid="config-needs-plan">
                {t("config.runDryRunFirst")}
              </Text>
            )}
          </Flex>
        </Flex>
      </Card>

      {error && (
        <Text size="2" color="red" data-testid="config-error">
          {t("config.error")}: {error}
        </Text>
      )}

      {plan && planIsCurrent && (
        <Card data-testid="config-plan" data-creates={plan.creates} data-updates={plan.updates}
              data-unchanged={plan.unchanged} data-removals={plan.removals}>
          <Flex direction="column" gap="3">
            <Text size="2" weight="medium" data-testid="config-plan-summary">
              {t("config.planSummary")} {planCounts(plan)}
            </Text>

            {plan.warnings && plan.warnings.length > 0 && (
              <Flex direction="column" gap="1" data-testid="config-plan-warnings">
                {plan.warnings.map((warning) => (
                  <Text size="1" color="amber" key={warning} data-testid="config-plan-warning">
                    {warning}
                  </Text>
                ))}
              </Flex>
            )}

            {plan.changes && plan.changes.length > 0 && (
              <Table.Root size="1" data-testid="config-plan-table">
                <Table.Header>
                  <Table.Row>
                    <Table.ColumnHeaderCell>{t("config.changeKind")}</Table.ColumnHeaderCell>
                    <Table.ColumnHeaderCell>{t("config.changeEntity")}</Table.ColumnHeaderCell>
                    <Table.ColumnHeaderCell>{t("config.changeRecord")}</Table.ColumnHeaderCell>
                    <Table.ColumnHeaderCell>{t("config.changeFields")}</Table.ColumnHeaderCell>
                  </Table.Row>
                </Table.Header>
                <Table.Body>
                  {plan.changes.map((change, index) => (
                    <Table.Row
                      key={`${change.entity}-${change.id}-${index}`}
                      data-testid="config-plan-row"
                      data-kind={change.kind}
                      data-entity={change.entity}
                    >
                      <Table.Cell>{t(`config.kind_${change.kind}`)}</Table.Cell>
                      <Table.Cell>{change.entity}</Table.Cell>
                      <Table.Cell>{change.name || change.id}</Table.Cell>
                      <Table.Cell>{(change.fields ?? []).join(", ")}</Table.Cell>
                    </Table.Row>
                  ))}
                </Table.Body>
              </Table.Root>
            )}
          </Flex>
        </Card>
      )}

      {result && (
        <Card data-testid="config-result" data-creates={result.creates} data-updates={result.updates}>
          <Flex direction="column" gap="2">
            <Text size="2" weight="medium" data-testid="config-result-summary">
              {t("config.resultSummary")} {planCounts(result)}
            </Text>
            {/* Removal is never applied, and the result says so rather than leaving the operator to
                wonder whether it happened. */}
            {result.removals > 0 && (
              <Text size="1" color="gray" data-testid="config-result-removals">
                {t("config.removalsNotDeleted").replace("{{count}}", String(result.removals))}
              </Text>
            )}
          </Flex>
        </Card>
      )}
    </Flex>
  );
};

export default ConfigExportPage;
