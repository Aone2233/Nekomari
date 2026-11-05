import { useCallback, useEffect, useRef, useState } from "react";
import { Badge, Button, Card, Flex, Text } from "@radix-ui/themes";
import { History } from "lucide-react";
import { useTranslation } from "react-i18next";
import { z } from "zod";
import { useNodeDetails } from "@/contexts/NodeDetailsContext";

const nonnegative = z.number().int().nonnegative();
const taskCursor = z.string().min(1).max(512).regex(/^[A-Za-z0-9_-]+$/);
const resultCursor = z.string().min(1).max(512).regex(/^[A-Za-z0-9_-]+$/);
const date = z.string().nullable();
const summarySchema = z.object({
  task_id: z.string().min(1).max(36), target_count: nonnegative.nullable(),
  result_count: nonnegative.max(1000), pending_count: nonnegative.max(1000),
  success_count: nonnegative.max(1000), failed_count: nonnegative.max(1000),
  unknown_count: nonnegative.max(1000), counts_truncated: z.boolean(),
  earliest_result_time: date, time_incomplete: z.boolean(),
  status: z.enum(["retained", "incomplete", "no_retained_results"]),
});
const pageSchema = z.object({ tasks: z.array(summarySchema).max(20), next_cursor: taskCursor.optional() });
const resultSchema = z.object({
  cursor: resultCursor, client: z.string().max(128), client_truncated: z.boolean(), created_at: date,
  finished_at: date, exit_code: z.number().int().nullable(), output_bytes: nonnegative,
});
const detailSchema = z.object({ task_id: z.string().min(1).max(36), results: z.array(resultSchema).max(20), next_cursor: resultCursor.optional() });
const chunkSchema = z.object({
  data_base64: z.string().max(22000), offset: nonnegative, next_offset: nonnegative,
  total_bytes: nonnegative, has_more: z.boolean(),
});
type Page = z.infer<typeof pageSchema>;
type Detail = z.infer<typeof detailSchema>;
type Chunk = z.infer<typeof chunkSchema>;
const maxCursorPages = 100;

function appendCursor<T>(values: T[], value: T): T[] {
  return [...values, value].slice(-maxCursorPages);
}

async function readHistory<T>(method: string, params: object, schema: z.ZodType<T>, signal: AbortSignal): Promise<T> {
  const response = await fetch("/api/rpc2", {
    method: "POST", credentials: "same-origin", cache: "no-store", signal,
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ jsonrpc: "2.0", id: 1, method, params }),
  });
  if (!response.ok) throw new Error("history unavailable");
  const envelope: unknown = await response.json();
  const parsed = z.object({ jsonrpc: z.literal("2.0"), id: z.literal(1), result: schema }).safeParse(envelope);
  if (!parsed.success) throw new Error("invalid history response");
  return parsed.data.result;
}

function formatTime(value: string | null): string | null {
  if (value === null || !Number.isFinite(Date.parse(value))) return null;
  return new Date(value).toLocaleString();
}

function ChunkViewer({ taskID, kind, resultCursor }: { taskID: string; kind: "command" | "output"; resultCursor?: string }) {
  const { t } = useTranslation();
  const [chunk, setChunk] = useState<Chunk | null>(null);
  const [text, setText] = useState("");
  const [offsets, setOffsets] = useState<number[]>([0]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(false);
  const request = useRef<AbortController | null>(null);
  useEffect(() => () => request.current?.abort(), []);

  const load = async (offset: number, nextOffsets: number[]) => {
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    setLoading(true);
    setError(false);
    try {
      const value = await readHistory("admin:readExecHistoryChunk", {
        task_id: taskID, kind, result_cursor: resultCursor ?? "", offset,
      }, chunkSchema, controller.signal);
      const binary = atob(value.data_base64);
      const bytes = Uint8Array.from(binary, (item) => item.charCodeAt(0));
      if (value.offset !== offset || value.next_offset !== offset + bytes.length ||
          bytes.length > 16384 || value.has_more !== (value.next_offset < value.total_bytes)) {
        throw new Error("invalid chunk boundaries");
      }
      if (controller.signal.aborted) return;
      setChunk(value);
      setText(new TextDecoder("utf-8").decode(bytes));
      setOffsets(nextOffsets);
    } catch {
      if (!controller.signal.aborted) {
        setChunk(null);
        setText("");
        setError(true);
      }
    } finally {
      if (!controller.signal.aborted) setLoading(false);
    }
  };

  return (
    <div className="min-w-0 space-y-2">
      {!chunk && <Button size="1" variant="soft" disabled={loading} onClick={() => void load(0, [0])}>
        {t(kind === "command" ? "execHistory.showCommand" : "execHistory.showOutput")}
      </Button>}
      {loading && <Text size="2" role="status">{t("execHistory.loading")}</Text>}
      {error && <Text size="2" color="red" role="alert">{t("execHistory.error")}</Text>}
      {chunk && <>
        <Text as="p" size="1" color="gray">{t("execHistory.chunkPosition", { start: chunk.offset, end: chunk.next_offset, total: chunk.total_bytes })}</Text>
        <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-words rounded-md bg-[var(--gray-2)] p-3 font-mono text-sm">{text || t("execHistory.emptyText")}</pre>
        <Flex gap="2" wrap="wrap">
          <Button size="1" variant="soft" disabled={loading || offsets.length <= 1} onClick={() => {
            const previous = offsets.slice(0, -1);
            void load(previous[previous.length - 1], previous);
          }}>{t("execHistory.previousChunk")}</Button>
          <Button size="1" variant="soft" disabled={loading || !chunk.has_more} onClick={() => void load(chunk.next_offset, appendCursor(offsets, chunk.next_offset))}>
            {t("execHistory.nextChunk")}
          </Button>
          <Button size="1" variant="ghost" onClick={() => { request.current?.abort(); setChunk(null); setText(""); setLoading(false); setError(false); setOffsets([0]); }}>
            {t("execHistory.hide")}
          </Button>
        </Flex>
      </>}
    </div>
  );
}

function TaskDetail({ taskID }: { taskID: string }) {
  const { t } = useTranslation();
  const { nodeDetail } = useNodeDetails();
  const [detail, setDetail] = useState<Detail | null>(null);
  const [cursorStack, setCursorStack] = useState<string[]>([""]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(false);
  const request = useRef<AbortController | null>(null);
  useEffect(() => () => request.current?.abort(), []);

  const fetchDetail = useCallback(async (cursorValue: string, stack: string[], controller: AbortController) => {
    return readHistory("admin:getExecHistoryDetail", { task_id: taskID, cursor: cursorValue }, detailSchema, controller.signal).then((value) => {
      if (value.task_id !== taskID) throw new Error("mismatched task");
      if (!controller.signal.aborted) { setDetail(value); setCursorStack(stack); }
    }).catch(() => {
      if (!controller.signal.aborted) setError(true);
    }).finally(() => {
      if (!controller.signal.aborted) setLoading(false);
    });
  }, [taskID]);

  const load = (cursorValue: string, stack: string[]) => {
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    setLoading(true);
    setError(false);
    setDetail(null);
    void fetchDetail(cursorValue, stack, controller);
  };

  useEffect(() => {
    const controller = new AbortController();
    request.current = controller;
    void fetchDetail("", [""], controller);
    return () => controller.abort();
  }, [fetchDetail]);
  return <div className="min-w-0 space-y-3 border-t border-[var(--gray-a5)] pt-3">
    <Text as="p" size="2" weight="medium">{t("execHistory.command")}</Text>
    <ChunkViewer taskID={taskID} kind="command" />
    <Text as="p" size="1" color="gray">{t("execHistory.liveChunks")}</Text>
    {loading && <Text size="2" role="status">{t("execHistory.loading")}</Text>}
    {error && <Flex gap="2" align="center"><Text size="2" color="red" role="alert">{t("execHistory.error")}</Text><Button size="1" onClick={() => void load(cursorStack[cursorStack.length - 1], cursorStack)}>{t("execHistory.retry")}</Button></Flex>}
    {detail && <>
      {detail.results.length === 0 && <Text size="2" color="gray">{t("execHistory.noResults")}</Text>}
      {detail.results.map((result) => {
        const name = nodeDetail.find((node) => node.uuid === result.client)?.name || result.client;
        const state = result.finished_at === null ? "pending" : result.exit_code === null ? "unknown" : result.exit_code === 0 ? "success" : "failed";
        return <Card key={result.cursor} className="min-w-0 p-3">
          <Flex direction="column" gap="2">
            <Flex align="center" gap="2" wrap="wrap"><Text weight="medium" className="break-all">{name}{result.client_truncated ? "…" : ""}</Text><Badge color={state === "failed" ? "red" : state === "success" ? "green" : "gray"}>{t(`execHistory.${state}`)}</Badge></Flex>
            {result.exit_code !== null && <Text size="1" color="gray">{t("execHistory.exitCode", { code: result.exit_code })}</Text>}
            {result.finished_at && <Text size="1" color="gray">{t("execHistory.finishedAt", { time: formatTime(result.finished_at) ?? t("execHistory.unknown") })}</Text>}
            <ChunkViewer taskID={taskID} kind="output" resultCursor={result.cursor} />
          </Flex>
        </Card>;
      })}
      <Flex gap="2" wrap="wrap">
        <Button size="1" variant="soft" disabled={loading || cursorStack.length <= 1} onClick={() => {
          const previous = cursorStack.slice(0, -1); void load(previous[previous.length - 1], previous);
        }}>{t("execHistory.previous")}</Button>
        <Button size="1" variant="soft" disabled={loading || !detail.next_cursor} onClick={() => {
          if (detail.next_cursor) void load(detail.next_cursor, appendCursor(cursorStack, detail.next_cursor));
        }}>{t("execHistory.next")}</Button>
      </Flex>
    </>}
  </div>;
}

export default function ExecHistory() {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [page, setPage] = useState<Page | null>(null);
  const [cursorStack, setCursorStack] = useState<string[]>([""]);
  const [cursorIndex, setCursorIndex] = useState(0);
  const [selected, setSelected] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(false);
  const request = useRef<AbortController | null>(null);
  useEffect(() => () => request.current?.abort(), []);

  const load = async (cursorValue: string, stack: string[], index: number) => {
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    setLoading(true); setError(false); setPage(null); setSelected(null);
    try {
      const value = await readHistory("admin:listExecHistory", { cursor: cursorValue }, pageSchema, controller.signal);
      if (!controller.signal.aborted) { setPage(value); setCursorStack(stack); setCursorIndex(index); }
    } catch {
      if (!controller.signal.aborted) setError(true);
    } finally {
      if (!controller.signal.aborted) setLoading(false);
    }
  };

  return <Card className="min-w-0 p-6" data-testid="exec-history">
    <Flex direction="column" gap="3">
      <Flex justify="between" align="center" gap="3" wrap="wrap">
        <Text size="4" weight="medium">{t("execHistory.title")}</Text>
        <Button variant="soft" onClick={() => {
          if (open) { request.current?.abort(); setPage(null); setSelected(null); setLoading(false); setError(false); setOpen(false); }
          else { setOpen(true); void load("", [""], 0); }
        }}><History size={16} />{t(open ? "execHistory.close" : "execHistory.open")}</Button>
      </Flex>
      {open && <>
        <Text size="2" color="orange">{t("execHistory.warning")}</Text>
        <Text size="2" color="gray">{t("execHistory.ordering")}</Text>
        {loading && <Text size="2" role="status">{t("execHistory.loading")}</Text>}
        {error && <Flex gap="2" align="center"><Text size="2" color="red" role="alert">{t("execHistory.error")}</Text><Button size="1" onClick={() => void load(cursorStack[cursorIndex], cursorStack, cursorIndex)}>{t("execHistory.retry")}</Button></Flex>}
        {page && <>
          {page.tasks.length === 0 && <Text size="2" color="gray">{t("execHistory.empty")}</Text>}
          {page.tasks.map((task) => <Card key={task.task_id} className="min-w-0 p-3">
            <Flex direction="column" gap="2">
              <Flex align="center" justify="between" gap="2" wrap="wrap">
                <Text weight="medium" className="break-all">{task.task_id}</Text>
                <Button size="1" variant="soft" aria-expanded={selected === task.task_id} onClick={() => setSelected(selected === task.task_id ? null : task.task_id)}>{t(selected === task.task_id ? "execHistory.hideDetails" : "execHistory.showDetails")}</Button>
              </Flex>
              <Text size="2" color="gray">{t("execHistory.targets", { count: task.target_count ?? t("execHistory.unknown") })} · {t("execHistory.counts", { pending: task.pending_count, success: task.success_count, failed: task.failed_count, unknown: task.unknown_count })}</Text>
              <Text size="1" color="gray">{task.earliest_result_time ? t("execHistory.earliest", { time: formatTime(task.earliest_result_time) ?? t("execHistory.unknown") }) : t("execHistory.timeUnknown")}</Text>
              {task.status !== "retained" && <Text size="1" color="orange">{t(task.status === "no_retained_results" ? "execHistory.noRetained" : "execHistory.incomplete")}</Text>}
              {task.counts_truncated && <Text size="1" color="orange">{t("execHistory.countsTruncated")}</Text>}
              {task.time_incomplete && task.earliest_result_time && <Text size="1" color="gray">{t("execHistory.timeIncomplete")}</Text>}
              {selected === task.task_id && <TaskDetail key={task.task_id} taskID={task.task_id} />}
            </Flex>
          </Card>)}
          <Flex gap="2" wrap="wrap">
            <Button size="1" variant="soft" disabled={loading || cursorIndex === 0} onClick={() => {
              void load(cursorStack[cursorIndex - 1], cursorStack, cursorIndex - 1);
            }}>{t("execHistory.newer")}</Button>
            <Button size="1" variant="soft" disabled={loading || !page.next_cursor} onClick={() => {
              if (page.next_cursor) {
                const next = appendCursor(cursorStack.slice(0, cursorIndex + 1), page.next_cursor);
                void load(page.next_cursor, next, next.length - 1);
              }
            }}>{t("execHistory.older")}</Button>
            <Button size="1" variant="ghost" disabled={loading} onClick={() => void load("", [""], 0)}>{t("execHistory.refresh")}</Button>
          </Flex>
        </>}
      </>}
    </Flex>
  </Card>;
}
