import { createRoot } from "react-dom/client";
import i18next from "i18next";
import { I18nextProvider } from "react-i18next";
import { Theme } from "@radix-ui/themes";
import { Toaster } from "sonner";
import { NodeDetailsContext, type NodeDetail } from "../src/contexts/NodeDetailsContext";
import { PingTaskContext, usePingTask } from "../src/contexts/PingTaskContext";
import { PingTaskProvider } from "../src/contexts/PingTaskProvider";
import { AddButton } from "../src/pages/admin/pingTask";
import { TaskView } from "../src/pages/admin/pingTask_Task";
import { ServerView } from "../src/pages/admin/pingTask_Server";
import translations from "../src/i18n/locales/en.json";
import "@radix-ui/themes/styles.css";

const i18n = i18next.createInstance();
await i18n.init({ lng: "en", resources: { en: { translation: translations } } });
const nodes = ["alpha", "beta", "gamma"].map((uuid, weight) => ({ uuid, name: uuid, weight })) as NodeDetail[];
const task = { id: 7, name: "IPv6 fixture", type: "auto", target: "example.test:80", reference: "gateway", target_family: "ipv6" as const, interval: 60, clients: ["alpha", "beta"] };
const legacyTask = { id: 8, name: "Legacy fixture", type: "tcp", target: "legacy.example.test:80", interval: 60, clients: ["alpha"] };
const calls: unknown[] = [];
let status = 200;
let body: unknown = { status: "success", data: { task_id: 8 } };
let refreshes = 0;
let held: { resolve: (response: Response) => void; status: number; body: unknown } | null = null;
let holdNext = false;
const scheduleRequests: Array<(response: Response) => void> = [];
window.fetch = async (input, init) => {
  if (String(input) === "/api/admin/ping/schedule-state") {
    return new Promise<Response>((resolve) => { scheduleRequests.push(resolve); });
  }
  if (String(input) === "/api/admin/ping") {
    return new Response(JSON.stringify({ status: "success", data: [] }), { status: 200 });
  }
  calls.push({ url: String(input), body: init?.body ? JSON.parse(String(init.body)) : null });
  if (holdNext) {
    holdNext = false;
    return new Promise<Response>((resolve) => { held = { resolve, status, body }; });
  }
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
};
Object.assign(window, { pingFamilyFixture: {
  calls: () => calls,
  refreshes: () => refreshes,
  respond: (nextStatus: number, nextBody: unknown) => { status = nextStatus; body = nextBody; },
  holdNext: () => { holdNext = true; },
  release: () => {
    if (!held) throw new Error("no held response");
    held.resolve(new Response(JSON.stringify(held.body), { status: held.status, headers: { "Content-Type": "application/json" } }));
    held = null;
  },
  scheduleRequests: () => scheduleRequests.length,
  replySchedule: (index: number, pending: boolean) => {
    const resolve = scheduleRequests[index];
    if (!resolve) throw new Error("missing schedule request");
    resolve(new Response(JSON.stringify({ status: "success", data: { reload_pending: pending } }), { status: 200 }));
  },
} });
export const ScheduleProbe = () => {
  const { schedulePending, refresh } = usePingTask();
  return <section id="schedule-probe"><output>{schedulePending === null ? "unknown" : String(schedulePending)}</output>
    <button onClick={refresh}>Refresh schedule</button></section>;
};
createRoot(document.getElementById("root")!).render(
  <I18nextProvider i18n={i18n}>
    <Theme>
      <NodeDetailsContext.Provider value={{ nodeDetail: nodes, isLoading: false, error: null, refresh: () => {} }}>
        <PingTaskProvider><ScheduleProbe /></PingTaskProvider>
        <PingTaskContext.Provider value={{ pingTasks: [task], isLoading: false, error: null, refresh: () => { refreshes++; } }}>
          <section id="legacy"><AddButton /></section>
          <section id="draft"><AddButton pinnedFamilyAvailable /></section>
          <TaskView pingTasks={[task]} />
          <section id="legacy-task"><TaskView pingTasks={[legacyTask]} /></section>
          <ServerView pingTasks={[task]} />
          <Toaster />
        </PingTaskContext.Provider>
      </NodeDetailsContext.Provider>
    </Theme>
  </I18nextProvider>,
);
