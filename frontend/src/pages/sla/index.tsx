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

interface SlaReportPageProps {
  initialWindow?: SlaWindow;
  onWindowChange?: (window: SlaWindow) => void;
}

export const SlaReportPage: React.FC<SlaReportPageProps> = ({ initialWindow, onWindowChange: updateLocation }) => {
  const { t } = useTranslation();
  const { call } = useRPC2Call();
  const { nodeList } = useNodeList();

  const [selectedWindow, setSelectedWindow] = useState<SlaWindow>(() =>
    initialWindow ?? windowFromLocation(typeof globalThis.location === "undefined" ? "" : globalThis.location.search),
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

  // The request is issued here, with the state updates inside the promise callbacks
  // rather than in a `load` helper the effect calls: that is the shape
  // react-hooks/set-state-in-effect rejects, and the reason `npm run audit:compiler`
  // was failing (exit 1) on this file. The `cancelled` flag additionally stops a slow
  // response for a window the reader has already left from overwriting the new one.
  useEffect(() => {
    let cancelled = false;
    call<{ window: SlaWindow }, SlaReport>("public:getSlaReport", { window: selectedWindow })
      .then((result) => {
        if (cancelled) return;
        setReport(result ?? null);
        setError(null);
      })
      .catch((cause) => {
        // An error is shown rather than swallowed: an empty table and a failed request
        // look identical to a reader, and only one of them means "nothing is wrong".
        if (cancelled) return;
        setError(cause instanceof Error ? cause.message : String(cause));
        setReport(null);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [call, selectedWindow]);

  // The parameter is not called `window`: shadowing the global here means
  // `window.location` and `window.history` resolve to the string, and the URL update
  // silently does nothing.
  const onWindowChange = useCallback((nextWindow: SlaWindow) => {
    // 换窗口时立刻回到加载态并清掉上一个窗口的错误，否则旧窗口的报错会挂在新窗口上。
    // 这是事件处理器，不是 effect body，所以在这里同步 setState 没有问题。
    setLoading(true);
    setError(null);
    setSelectedWindow(nextWindow);
    if (updateLocation) {
      updateLocation(nextWindow);
      return;
    }
    if (typeof globalThis === "undefined" || typeof globalThis.location === "undefined") return;
    const url = new URL(globalThis.location.href);
    url.searchParams.set("window", nextWindow);
    globalThis.history.replaceState(null, "", url.toString());
  }, [updateLocation]);

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
