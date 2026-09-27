import {
  Badge,
  Button,
  Card,
  Flex,
  Heading,
  Select,
  Table,
  Text,
  TextField,
} from "@radix-ui/themes";
import { useTranslation } from "react-i18next";
import React, { useCallback, useMemo, useState } from "react";
import { localInputValue, remainingText, toRFC3339 } from "./maintenanceFormat";

/**
 * Maintenance windows (roadmap H3).
 *
 * Takes the window list and the write callbacks as props, so the mounted fixture can put an open
 * window, a scoped one, an expired one and a refused save on screen without a server.
 *
 * Three presentation rules come from the server's rules, and each is a place this page could undo
 * the care behind them:
 *
 *   - **"Open" is the server's answer, not a client-side date comparison.** `open` and
 *     `remaining_seconds` arrive computed; recomputing them here would be a second definition that
 *     could disagree with the one the notifier uses.
 *   - **A window with no node list is fleet-wide, and says so.** "Covers everything" is a bigger
 *     claim than "covers these three", and an operator scanning the list needs to see which one
 *     they made.
 *   - **A refusal keeps the form's values.** Clearing the form when a save is refused loses the
 *     window the operator had just described, and they have to type the times again.
 */

export interface MaintenanceWindow {
  id: number;
  name: string;
  start: string;
  end: string;
  clients: string[];
  reason?: string;
  open: boolean;
  covers_everything: boolean;
  remaining_seconds: number;
}

export interface MaintenancePageProps {
  windows: MaintenanceWindow[];
  /** save creates (id 0) or updates a window. Rejects with the server's reason. */
  save?: (window: {
    id: number;
    name: string;
    start: string;
    end: string;
    clients: string[];
    reason: string;
  }) => Promise<void>;
  remove?: (id: number) => Promise<void>;
  /** nodeUUIDs is the fleet, for the scope picker. */
  nodeUUIDs?: string[];
  loading?: boolean;
}

const emptyDraft = () => ({ id: 0, name: "", start: "", end: "", clients: [] as string[], reason: "" });

export const MaintenancePage: React.FC<MaintenancePageProps> = ({
  windows,
  save,
  remove,
  nodeUUIDs,
  loading = false,
}) => {
  const { t } = useTranslation();
  const [draft, setDraft] = useState(emptyDraft);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const openCount = useMemo(() => windows.filter((window) => window.open).length, [windows]);

  const onSave = useCallback(async () => {
    if (!save) return;
    setBusy(true);
    setError(null);
    try {
      await save({
        id: draft.id,
        name: draft.name,
        start: toRFC3339(draft.start),
        end: toRFC3339(draft.end),
        clients: draft.clients,
        reason: draft.reason,
      });
      // Only cleared on success: a refusal keeps the window the operator described.
      setDraft(emptyDraft());
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setBusy(false);
    }
  }, [draft, save]);

  const onDelete = useCallback(
    async (id: number) => {
      if (!remove) return;
      setError(null);
      try {
        await remove(id);
      } catch (cause) {
        setError(cause instanceof Error ? cause.message : String(cause));
      }
    },
    [remove],
  );

  const onEdit = useCallback((window: MaintenanceWindow) => {
    setError(null);
    setDraft({
      id: window.id,
      name: window.name,
      start: localInputValue(window.start),
      end: localInputValue(window.end),
      clients: window.clients ?? [],
      reason: window.reason ?? "",
    });
  }, []);

  return (
    <Flex direction="column" gap="4" p="4" data-testid="maintenance-page">
      <Flex direction="column" gap="1">
        <Heading size="6">{t("maintenance.title")}</Heading>
        <Text size="2" color="gray">
          {t("maintenance.subtitle")}
        </Text>
      </Flex>

      <Card data-testid="maintenance-form">
        <Flex direction="column" gap="3">
          <Flex align="center" gap="3" wrap="wrap">
            <Text size="2" weight="medium">
              {draft.id ? t("maintenance.editing") : t("maintenance.creating")}
            </Text>
            <Badge data-testid="maintenance-draft-id" data-id={draft.id}>
              {draft.id ? `#${draft.id}` : t("maintenance.new")}
            </Badge>
          </Flex>

          <Flex gap="3" wrap="wrap" align="center">
            <TextField.Root
              placeholder={t("maintenance.namePlaceholder")}
              value={draft.name}
              onChange={(event) => setDraft({ ...draft, name: event.target.value })}
              data-testid="maintenance-input-name"
            />
            <TextField.Root
              type="datetime-local"
              value={draft.start}
              onChange={(event) => setDraft({ ...draft, start: event.target.value })}
              data-testid="maintenance-input-start"
            />
            <TextField.Root
              type="datetime-local"
              value={draft.end}
              onChange={(event) => setDraft({ ...draft, end: event.target.value })}
              data-testid="maintenance-input-end"
            />
            <TextField.Root
              placeholder={t("maintenance.reasonPlaceholder")}
              value={draft.reason}
              onChange={(event) => setDraft({ ...draft, reason: event.target.value })}
              data-testid="maintenance-input-reason"
            />
          </Flex>

          <Flex gap="3" align="center" wrap="wrap">
            <Text size="2">{t("maintenance.scope")}</Text>
            <Select.Root
              value={draft.clients.length === 0 ? "__all__" : draft.clients[0]}
              onValueChange={(value) =>
                setDraft({ ...draft, clients: value === "__all__" ? [] : [value] })
              }
            >
              <Select.Trigger data-testid="maintenance-input-scope" />
              <Select.Content>
                <Select.Item value="__all__">{t("maintenance.scopeAll")}</Select.Item>
                {(nodeUUIDs ?? []).map((uuid) => (
                  <Select.Item key={uuid} value={uuid}>
                    {uuid}
                  </Select.Item>
                ))}
              </Select.Content>
            </Select.Root>
            <Badge
              data-testid="maintenance-draft-scope"
              data-all={String(draft.clients.length === 0)}
              color={draft.clients.length === 0 ? "amber" : "gray"}
            >
              {draft.clients.length === 0 ? t("maintenance.scopeAllBadge") : draft.clients[0]}
            </Badge>
            <Button
              onClick={() => void onSave()}
              disabled={!save || busy || draft.name === "" || draft.start === "" || draft.end === ""}
              loading={busy}
              data-testid="maintenance-save"
            >
              {t("maintenance.save")}
            </Button>
            {draft.id !== 0 && (
              <Button size="1" variant="soft" onClick={() => setDraft(emptyDraft())}
                      data-testid="maintenance-cancel">
                {t("maintenance.cancelEdit")}
              </Button>
            )}
          </Flex>

          {error && (
            <Text size="2" color="red" data-testid="maintenance-error">
              {t("maintenance.error")}: {error}
            </Text>
          )}
        </Flex>
      </Card>

      <Card data-testid="maintenance-list">
        <Flex direction="column" gap="3">
          <Flex align="center" gap="3">
            <Text size="2" weight="medium">
              {t("maintenance.windowsHeading")}
            </Text>
            <Badge data-testid="maintenance-open-count" data-count={openCount}
                   color={openCount ? "green" : "gray"}>
              {t("maintenance.openCount").replace("{{count}}", String(openCount))}
            </Badge>
          </Flex>

          {loading && windows.length === 0 ? (
            <Text size="2" color="gray" data-testid="maintenance-loading">
              {t("maintenance.loading")}
            </Text>
          ) : windows.length === 0 ? (
            <Text size="2" color="gray" data-testid="maintenance-empty">
              {t("maintenance.empty")}
            </Text>
          ) : (
            <Table.Root size="1">
              <Table.Header>
                <Table.Row>
                  <Table.ColumnHeaderCell>{t("maintenance.name")}</Table.ColumnHeaderCell>
                  <Table.ColumnHeaderCell>{t("maintenance.scope")}</Table.ColumnHeaderCell>
                  <Table.ColumnHeaderCell>{t("maintenance.window")}</Table.ColumnHeaderCell>
                  <Table.ColumnHeaderCell>{t("maintenance.state")}</Table.ColumnHeaderCell>
                  <Table.ColumnHeaderCell />
                </Table.Row>
              </Table.Header>
              <Table.Body>
                {windows.map((window) => (
                  <Table.Row
                    key={window.id}
                    data-testid="maintenance-row"
                    data-id={window.id}
                    data-open={String(window.open)}
                    data-scope={window.covers_everything ? "all" : "scoped"}
                  >
                    <Table.Cell>
                      {window.name}
                      {window.reason ? (
                        <Text size="1" color="gray">
                          {" "}
                          — {window.reason}
                        </Text>
                      ) : null}
                    </Table.Cell>
                    <Table.Cell data-testid="maintenance-row-scope">
                      {window.covers_everything
                        ? t("maintenance.scopeAllBadge")
                        : (window.clients ?? []).join(", ")}
                    </Table.Cell>
                    <Table.Cell>
                      {localInputValue(window.start)} → {localInputValue(window.end)}
                    </Table.Cell>
                    <Table.Cell data-testid="maintenance-row-state">
                      {window.open
                        ? t("maintenance.openFor").replace(
                            "{{remaining}}",
                            remainingText(window.remaining_seconds),
                          )
                        : t("maintenance.closed")}
                    </Table.Cell>
                    <Table.Cell>
                      <Flex gap="2">
                        <Button size="1" variant="soft" onClick={() => onEdit(window)}
                                data-testid="maintenance-edit">
                          {t("maintenance.edit")}
                        </Button>
                        <Button size="1" variant="soft" color="red"
                                onClick={() => void onDelete(window.id)}
                                data-testid="maintenance-delete">
                          {t("maintenance.delete")}
                        </Button>
                      </Flex>
                    </Table.Cell>
                  </Table.Row>
                ))}
              </Table.Body>
            </Table.Root>
          )}
        </Flex>
      </Card>
    </Flex>
  );
};

export default MaintenancePage;
