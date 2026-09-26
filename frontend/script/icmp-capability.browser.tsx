import { createRoot } from "react-dom/client";
import i18next from "i18next";
import { I18nextProvider } from "react-i18next";
import { Theme } from "@radix-ui/themes";
import { IcmpCapabilityBadge } from "../src/components/admin/IcmpCapabilityBadge";
import "@radix-ui/themes/styles.css";

// One badge per state, side by side. The states are what this fixture is for:
// `raw` and `ping` are both "ICMP works", `none` is "this node cannot probe", and
// absent is "the agent has not said" — which must not look like `none`, because
// every not-yet-upgraded node is in that state and painting them as broken is the
// exact mistake roadmap E2 is about.
//
// English resources only; the assertions read the DOM attributes and the visible
// text, not the translated strings.

const i18n = i18next.createInstance();
await i18n.init({
  lng: "en",
  resources: {
    en: {
      translation: {
        "admin.nodeDetail.icmp.raw": "ICMP available: raw socket",
        "admin.nodeDetail.icmp.ping": "ICMP available: unprivileged ping socket",
        "admin.nodeDetail.icmp.noneLabel": "ICMP unavailable",
        "admin.nodeDetail.icmp.none":
          "This node cannot send ICMP probes: neither socket opens. Loss shown for it is the tool's limit. " +
          "Fix: setcap cap_net_raw+ep on the agent binary, or widen net.ipv4.ping_group_range.",
        "admin.nodeDetail.icmp.unknownLabel": "ICMP unknown",
        "admin.nodeDetail.icmp.unknown":
          "This node has not reported its ICMP capability yet. Unknown is not unavailable.",
      },
    },
  },
});

const CASES: Array<{ id: string; capability: "raw" | "ping" | "none" | "" | undefined }> = [
  { id: "raw", capability: "raw" },
  { id: "ping", capability: "ping" },
  { id: "none", capability: "none" },
  { id: "empty", capability: "" },
  { id: "absent", capability: undefined },
];

createRoot(document.getElementById("root")!).render(
  <I18nextProvider i18n={i18n}>
    <Theme>
      <div id="cases">
        {CASES.map((entry) => (
          <div key={entry.id} data-case={entry.id}>
            <IcmpCapabilityBadge capability={entry.capability} />
          </div>
        ))}
        <div data-case="compact-none">
          <IcmpCapabilityBadge capability="none" compact />
        </div>
      </div>
    </Theme>
  </I18nextProvider>,
);
