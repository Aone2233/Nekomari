import { useSyncExternalStore } from "react";
import { createRoot } from "react-dom/client";
import i18next from "i18next";
import { I18nextProvider } from "react-i18next";
import { Theme } from "@radix-ui/themes";
import { RPC2Context } from "../src/contexts/RPC2Context";
import type { RPC2Client } from "../src/lib/rpc2";
import FileManagerPanel from "../src/pages/terminal/FileManagerPanel";
import FileEditorDialog from "../src/pages/terminal/FileEditorDialog";
import type { RemoteFileInfo } from "../src/pages/terminal/fileManagerApi";
import "@radix-ui/themes/styles.css";
import "../src/global.css";

const file = (path: string, isDir = false): RemoteFileInfo => ({
  name: path.split("/").at(-1) || path,
  path,
  is_dir: isDir,
  is_symlink: false,
  size: isDir ? 0 : 12,
  mode: isDir ? "drwxr-xr-x" : "-rw-r--r--",
  mode_octal: isDir ? "0755" : "0644",
  uid: 1000,
  gid: 1000,
  owner: "fixture",
  group: "fixture",
  modified_at: "2026-09-23T00:00:00Z",
});

type Params = { uuid: string; path?: string; source?: string; destination?: string };
type HeldList = { resolve: (files: RemoteFileInfo[]) => void };
const directories: Record<string, Record<string, RemoteFileInfo[]>> = {
  A: { "/": [file("/alpha.txt"), file("/drafts", true)], "/slow": [] },
  B: { "/": [file("/beta.txt")] },
};
const calls: Array<{ method: string; params: Params }> = [];
const heldLists = new Set<string>();
const pendingLists = new Map<string, HeldList>();
const heldReads = new Set<string>();
const pendingReads = new Map<string, (text: string) => void>();
const keyFor = (uuid: string, path: string) => `${uuid}:${path}`;
const copyFiles = (items: RemoteFileInfo[]) => items.map((item) => ({ ...item }));

const client = {
  call: async (method: string, params: Params) => {
    calls.push({ method, params: { ...params } });
    if (!fixtureState.connected) throw new Error("Disconnected fixture RPC");
    if (method === "admin:fileList") {
      const key = keyFor(params.uuid, params.path || "/");
      if (heldLists.has(key)) {
        heldLists.delete(key);
        return new Promise<RemoteFileInfo[]>((resolve) => {
          pendingLists.set(key, { resolve });
        });
      }
      return copyFiles(directories[params.uuid]?.[params.path || "/"] || []);
    }
    if (method === "admin:fileMove") {
      const source = params.source!;
      const destination = params.destination!;
      const parent = source.slice(0, source.lastIndexOf("/")) || "/";
      const entries = directories[params.uuid][parent];
      const entry = entries.find((item) => item.path === source);
      if (!entry) throw new Error(`Missing file: ${source}`);
      directories[params.uuid][parent] = entries.map((item) =>
        item === entry ? { ...item, path: destination, name: destination.split("/").at(-1)! } : item,
      );
      return null;
    }
    if (method === "admin:fileDelete") {
      const source = params.path!;
      const parent = source.slice(0, source.lastIndexOf("/")) || "/";
      directories[params.uuid][parent] = directories[params.uuid][parent].filter((item) => item.path !== source);
      return null;
    }
    throw new Error(`Unexpected RPC: ${method}`);
  },
} as unknown as RPC2Client;

const nativeFetch = window.fetch.bind(window);
window.fetch = (input, init) => {
  const url = new URL(String(input), window.location.href);
  if (!url.pathname.includes("/file/download")) return nativeFetch(input, init);
  const path = url.searchParams.get("path") || "";
  const respond = (text: string) => new Response(text, {
    status: 200,
    headers: { "Content-Type": "text/plain", "Content-Length": String(new TextEncoder().encode(text).byteLength) },
  });
  if (heldReads.has(path)) {
    return new Promise<Response>((resolve) => {
      pendingReads.set(path, (text) => resolve(respond(text)));
    });
  }
  return Promise.resolve(respond(`content of ${path}`));
};

type FixtureState = { node: string; view: "panel" | "editor"; editorPath: string; connected: boolean };
let fixtureState: FixtureState = { node: "A", view: "panel", editorPath: "/one.md", connected: true };
const listeners = new Set<() => void>();
const subscribe = (listener: () => void) => {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
};
const getSnapshot = () => fixtureState;
const updateFixture = (patch: Partial<FixtureState>) => {
  fixtureState = { ...fixtureState, ...patch };
  for (const listener of listeners) listener();
};
const i18n = i18next.createInstance();
await i18n.init({ lng: "en", resources: { en: { translation: {} } } });

export function Fixture() {
  const { node, view, editorPath, connected } = useSyncExternalStore(subscribe, getSnapshot);
  return (
    <I18nextProvider i18n={i18n}>
      <Theme>
        <RPC2Context.Provider value={{
          client, connectionState: connected ? "connected" : "disconnected", isConnected: connected,
          error: null, connect: async () => {}, disconnect: () => {},
        }}>
          <div data-testid="rpc-connection" data-state={connected ? "connected" : "disconnected"}>
            {view === "panel" ? <FileManagerPanel uuid={node} /> : (
              <FileEditorDialog open uuid={node} initialFile={file(editorPath)} onOpenChange={() => {}} />
            )}
          </div>
        </RPC2Context.Provider>
      </Theme>
    </I18nextProvider>
  );
}

Object.assign(window, {
  fileFixture: {
    calls: () => [...calls],
    setNode: (node: string) => updateFixture({ node }),
    setConnected: (connected: boolean) => updateFixture({ connected }),
    setDirectory: (uuid: string, path: string, names: string[]) => {
      directories[uuid][path] = names.map((name) => file(`${path.replace(/\/$/, "")}/${name}`));
    },
    setView: (view: "panel" | "editor") => updateFixture({ view }),
    setEditorFile: (path: string) => updateFixture({ editorPath: path }),
    holdList: (uuid: string, path: string) => heldLists.add(keyFor(uuid, path)),
    pendingList: (uuid: string, path: string) => pendingLists.has(keyFor(uuid, path)),
    resolveList: (uuid: string, path: string, names: string[]) => {
      const key = keyFor(uuid, path);
      const pending = pendingLists.get(key);
      if (!pending) throw new Error(`No pending list: ${key}`);
      pendingLists.delete(key);
      pending.resolve(names.map((name) => file(`${path.replace(/\/$/, "")}/${name}`)));
    },
    holdRead: (path: string) => heldReads.add(path),
    pendingRead: (path: string) => pendingReads.has(path),
    resolveRead: (path: string, text: string) => {
      const pending = pendingReads.get(path);
      if (!pending) throw new Error(`No pending read: ${path}`);
      pendingReads.delete(path);
      heldReads.delete(path);
      pending(text);
    },
  },
});

createRoot(document.getElementById("root")!).render(<Fixture />);
