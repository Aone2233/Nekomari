import { useSyncExternalStore } from "react";
import { createRoot } from "react-dom/client";
import i18next from "i18next";
import { I18nextProvider } from "react-i18next";
import { Theme } from "@radix-ui/themes";
import { RPC2Context } from "../src/contexts/RPC2Context";
import type { RPC2Client } from "../src/lib/rpc2";
import RemoteFileTree from "../src/pages/terminal/RemoteFileTree";
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

// One remote tree, rooted at /root:
//
//   /root
//     docs/
//       notes.md
//       deep/
//         leaf.txt
//     readme.txt
//     src/
//       main.ts
const tree: Record<string, RemoteFileInfo[]> = {
  "/root": [file("/root/docs", true), file("/root/readme.txt"), file("/root/src", true)],
  "/root/docs": [file("/root/docs/notes.md"), file("/root/docs/deep", true)],
  "/root/docs/deep": [file("/root/docs/deep/leaf.txt")],
  "/root/src": [file("/root/src/main.ts")],
  "/other": [file("/other/elsewhere.txt")],
};

type Params = { uuid: string; path?: string };
const calls: Array<{ method: string; params: Params }> = [];
const opened: string[] = [];

const client = {
  call: async (method: string, params: Params) => {
    calls.push({ method, params: { ...params } });
    if (method === "admin:fileList") {
      // A fresh array of fresh objects every time, so a test cannot pass by
      // observing an identity the component happened to keep.
      return (tree[params.path ?? "/"] ?? []).map((item) => ({ ...item }));
    }
    throw new Error(`Unexpected RPC: ${method}`);
  },
} as unknown as RPC2Client;

type FixtureState = {
  uuid: string;
  rootPath: string;
  refreshToken: number;
  revealPath: string | null;
  activePath: string | undefined;
};
let fixtureState: FixtureState = {
  uuid: "A",
  rootPath: "/root",
  refreshToken: 0,
  revealPath: null,
  activePath: undefined,
};
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
  const state = useSyncExternalStore(subscribe, getSnapshot);
  return (
    <I18nextProvider i18n={i18n}>
      <Theme>
        <RPC2Context.Provider value={{
          client, connectionState: "connected", isConnected: true,
          error: null, connect: async () => {}, disconnect: () => {},
        }}>
          <div data-testid="controls" className="flex gap-2 p-2">
            <button type="button" data-testid="root-other" onClick={() => updateFixture({ rootPath: "/other" })}>
              Root other
            </button>
            <button type="button" data-testid="refresh" onClick={() => updateFixture({ refreshToken: state.refreshToken + 1 })}>
              Refresh
            </button>
            <button type="button" data-testid="reveal" onClick={() => updateFixture({ revealPath: "/root/docs/deep/leaf.txt" })}>
              Reveal
            </button>
            <button type="button" data-testid="uuid-b" onClick={() => updateFixture({ uuid: "B" })}>
              Node B
            </button>
          </div>
          <div data-testid="tree" style={{ height: 400 }}>
            <RemoteFileTree
              uuid={state.uuid}
              rootPath={state.rootPath}
              activePath={state.activePath}
              refreshToken={state.refreshToken}
              revealPath={state.revealPath}
              onOpenFile={(openedFile) => opened.push(openedFile.path)}
            />
          </div>
        </RPC2Context.Provider>
      </Theme>
    </I18nextProvider>
  );
}

Object.assign(window, {
  treeFixture: {
    calls: () => [...calls],
    lists: (path?: string) => calls
      .filter((entry) => entry.method === "admin:fileList")
      .filter((entry) => path === undefined || entry.params.path === path),
    opened: () => [...opened],
    state: () => ({ ...fixtureState }),
  },
});

createRoot(document.getElementById("root")!).render(<Fixture />);
