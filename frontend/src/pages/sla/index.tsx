import { Flex, Heading, Text } from "@radix-ui/themes";
import { useTranslation } from "react-i18next";
import React, { useCallback, useEffect, useMemo, useState } from "react";
import { SlaReportTable } from "@/components/status/SlaReportTable";
import { useRPC2Call } from "@/contexts/RPC2Context";
import { useNodeList } from "@/contexts/NodeListContext";
import type { SlaReport, SlaWindow } from "@/types/Sla";
import { windowFromLocation } from "@/components/status/slaFormat";

/**
 * The public status page (roadmap H1).
 *
 * Guest-readable: it is under the public layout and calls a public:* method, so it can
 * be linked to someone without an account. That is the point of the feature — a report
 * you can send is worth more than a chart you can only look at.
 *
 * The page owns the fetch and the window; the table owns the presentation and takes the
 * report as a prop so the fixture can render every state without a server.
 *
 * Window selection is in the URL (`?window=7d`) so a report can be shared with its
 * window rather than only its host.
 */

/** windowFromLocation reads the window out of the URL, falling back to the default. */
export const SlaReportPage: React.FC = () => {
  const { t } = useTranslation();
  const { call } = useRPC2Call();
  const { nodeList } = useNodeList();

  const [selectedWindow, setSelectedWindow] = useState<SlaWindow>(() =>
    windowFromLocation(typeof globalThis.location === "undefined" ? "" : globalThis.location.search),
  );
  const [report, setReport] = useState<SlaReport | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const nodeNames = useMemo(() => {
    const names: Record<string, string> = {};
    for (const node of nodeList ?? []) {
      if (node.uuid) names[node.uuid] = node.name ?? node.uuid;
    }
    return names;
  }, [nodeList]);

  const load = useCallback(
    async (window: SlaWindow) => {
      setLoading(true);
      setError(null);
      try {
        const result = await call<{ window: SlaWindow }, SlaReport>("public:getSlaReport", {
          window,
        });
        setReport(result ?? null);
      } catch (cause) {
        // An error is shown rather than swallowed: an empty table and a failed request
        // look identical to a reader, and only one of them means "nothing is wrong".
        setError(cause instanceof Error ? cause.message : String(cause));
        setReport(null);
      } finally {
        setLoading(false);
      }
    },
    [call],
  );

  useEffect(() => {
    void load(selectedWindow);
  }, [load, selectedWindow]);

  // The parameter is not called `window`: shadowing the global here means
  // `window.location` and `window.history` resolve to the string, and the URL update
  // silently does nothing.
  const onWindowChange = useCallback((nextWindow: SlaWindow) => {
    setSelectedWindow(nextWindow);
    if (typeof globalThis === "undefined" || typeof globalThis.location === "undefined") return;
    const url = new URL(globalThis.location.href);
    url.searchParams.set("window", nextWindow);
    globalThis.history.replaceState(null, "", url.toString());
  }, []);

  return (
    <Flex direction="column" gap="4" p="4" data-testid="status-page">
      <Flex direction="column" gap="1">
        <Heading size="6">{t("status.title")}</Heading>
        <Text size="2" color="gray">
          {t("status.subtitle")}
        </Text>
      </Flex>
      <SlaReportTable
        report={report}
        loading={loading}
        error={error}
        window={selectedWindow}
        onWindowChange={onWindowChange}
        nodeNames={nodeNames}
      />
    </Flex>
  );
};

export default SlaReportPage;
