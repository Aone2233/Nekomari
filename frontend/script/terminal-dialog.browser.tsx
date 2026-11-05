import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import TerminalDialog from "../src/pages/terminal/TerminalDialog";
import "../src/global.css";

function Fixture() {
  const [kind, setKind] = useState<"quick" | "delete" | "nested" | "removed" | "unmount" | null>(null);
  const [child, setChild] = useState(false);
  const [immediateChild, setImmediateChild] = useState(false);
  const [triggerVisible, setTriggerVisible] = useState(true);
  const [busy, setBusy] = useState(false);
  const [live, setLive] = useState(true);
  const [value, setValue] = useState("");
  const [selected, setSelected] = useState("");
  const [submitted, setSubmitted] = useState(0);
  const [cancelled, setCancelled] = useState(0);
  const [outerEscape, setOuterEscape] = useState(0);
  useEffect(() => {
    const handler = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOuterEscape((count) => count + 1);
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, []);
  const close = () => { setCancelled((count) => count + 1); setKind(null); };

  return (
    <main>
      <button type="button" data-testid="outside" onClick={() => setKind("quick")}>Open quick</button>
      <button type="button" data-testid="delete" onClick={() => setKind("delete")}>Open delete</button>
      <button type="button" data-testid="nested" onClick={() => setKind("nested")}>Open nested</button>
      {triggerVisible && (
        <button type="button" data-testid="removed" onClick={() => setKind("removed")}>
          Open and remove trigger on close
        </button>
      )}
      <button type="button" data-testid="unmount" onClick={() => setKind("unmount")}>Open unmount dialog</button>
      <button type="button" data-testid="busy" onClick={() => setBusy((current) => !current)}>Toggle busy</button>
      <output data-testid="results" data-submit={submitted} data-cancel={cancelled} data-selected={selected} data-outer-escape={outerEscape} />
      <TerminalDialog
        open={kind === "quick"}
        title="Quick open"
        description="Choose a file"
        fields={[{
          key: "path", placeholder: "File path", value, onChange: setValue,
          suggestions: ["alpha.txt", "beta.txt"], onSelectSuggestion: setSelected,
        }]}
        autoHighlight
        selectSubmits
        submitting={busy}
        onSubmit={() => setSubmitted((count) => count + 1)}
        onCancel={close}
      />
      <TerminalDialog
        open={kind === "delete" || kind === "removed"}
        title="Delete item"
        description="This cannot be undone."
        destructive
        confirmLabel="Delete"
        submitting={busy}
        onSubmit={() => { setSubmitted((count) => count + 1); setKind(null); }}
        onCancel={() => { if (kind === "removed") setTriggerVisible(false); close(); }}
      />
      <TerminalDialog
        open={kind === "nested"}
        title="Parent dialog"
        secondaryLabel="Open child"
        onSecondary={() => { setChild(true); setImmediateChild(true); }}
        onSubmit={() => setSubmitted((count) => count + 1)}
        onCancel={() => {
          if (immediateChild) {
            setSubmitted((count) => count + 100);
            return;
          }
          close();
        }}
      />
      <TerminalDialog
        open={child}
        title="Child dialog"
        destructive
        onSubmit={() => setSubmitted((count) => count + 1)}
        onCancel={() => { setImmediateChild(false); setChild(false); }}
      />
      {live && (
        <TerminalDialog
          open={kind === "unmount"}
          title="Unmount on confirm"
          onSubmit={() => setLive(false)}
          onCancel={() => setKind(null)}
        />
      )}
    </main>
  );
}

createRoot(document.getElementById("root")!).render(<Fixture />);
