import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import i18next from "i18next";
import { I18nextProvider } from "react-i18next";
import { Theme } from "@radix-ui/themes";
import NodeSelectorDialog from "../src/components/NodeSelectorDialog";
import SelectorDialog from "../src/components/SelectorDialog";
import { NodeDetailsContext, type NodeDetail } from "../src/contexts/NodeDetailsContext";

declare global {
  interface Window {
    selectorFixture: {
      setGenericValue: (ids: string[]) => void;
      setNodeValue: (ids: string[]) => void;
      setControlledValue: (ids: string[]) => void;
      setControlledOpen: (open: boolean) => void;
      rerender: () => void;
    };
  }
}

const nodes = [
  { uuid: "alpha", name: "Alpha", weight: 1 },
  { uuid: "beta", name: "Beta", weight: 2 },
  { uuid: "gamma", name: "Gamma", weight: 3 },
] as NodeDetail[];

const i18n = i18next.createInstance();
await i18n.init({
  lng: "en",
  resources: { en: { translation: {
    "common.select": "Select",
    "common.cancel": "Cancel",
    "common.done": "Done",
    "common.search": "Search",
    "common.content": "Content",
    "common.server": "Server",
    "common.selected_total": "{{count}} of {{total}}",
    "common.select_all": "Select all",
    "common.deselect_all": "Deselect all",
  } } },
});

function Fixture() {
  const [genericValue, setGenericValue] = useState(["alpha"]);
  const [nodeValue, setNodeValue] = useState(["alpha"]);
  const [controlledValue, setControlledValue] = useState(["alpha"]);
  const [controlledOpen, setControlledOpen] = useState(false);
  const [genericCommits, setGenericCommits] = useState(0);
  const [nodeCommits, setNodeCommits] = useState(0);
  const [controlledCommits, setControlledCommits] = useState(0);
  const [rerenders, setRerenders] = useState(0);

  useEffect(() => {
    window.selectorFixture = {
      setGenericValue,
      setNodeValue,
      setControlledValue,
      setControlledOpen,
      rerender: () => setRerenders((count) => count + 1),
    };
  }, []);

  return (
    <I18nextProvider i18n={i18n}>
      <Theme>
        <NodeDetailsContext.Provider value={{
          nodeDetail: nodes,
          isLoading: false,
          error: null,
          refresh: () => {},
        }}>
          <output id="rerenders">{rerenders}</output>
          <section id="generic">
            <SelectorDialog
              value={[...genericValue]}
              onChange={(ids) => {
                setGenericCommits((count) => count + 1);
                setGenericValue(ids);
              }}
              items={nodes}
              getId={(node) => node.uuid}
              getLabel={(node) => node.name}
              title="Generic nodes"
              trigger={<button type="button">Choose generic</button>}
            />
            <output id="generic-value">{genericValue.join(",")}</output>
            <output id="generic-commits">{genericCommits}</output>
          </section>
          <section id="nodes">
            <NodeSelectorDialog
              value={[...nodeValue]}
              onChange={(ids) => {
                setNodeCommits((count) => count + 1);
                setNodeValue(ids);
              }}
              title="Uncontrolled nodes"
            ><button type="button">Choose nodes</button></NodeSelectorDialog>
            <output id="node-value">{nodeValue.join(",")}</output>
            <output id="node-commits">{nodeCommits}</output>
          </section>
          <section id="controlled">
            <NodeSelectorDialog
              open={controlledOpen}
              onOpenChange={setControlledOpen}
              value={[...controlledValue]}
              onChange={(ids) => {
                setControlledCommits((count) => count + 1);
                setControlledValue(ids);
              }}
              title="Controlled nodes"
            ><button type="button">Choose controlled</button></NodeSelectorDialog>
            <output id="controlled-value">{controlledValue.join(",")}</output>
            <output id="controlled-commits">{controlledCommits}</output>
            <output id="controlled-open">{String(controlledOpen)}</output>
          </section>
        </NodeDetailsContext.Provider>
      </Theme>
    </I18nextProvider>
  );
}

export { Fixture };

createRoot(document.getElementById("root")!).render(<Fixture />);
