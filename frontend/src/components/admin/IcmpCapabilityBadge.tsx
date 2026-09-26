import { Flex, Text } from "@radix-ui/themes";
import { useTranslation } from "react-i18next";
import { AlertTriangle, CircleHelp, ShieldCheck } from "lucide-react";

/**
 * What a node reported about its own ICMP sockets, and nothing more.
 *
 * Roadmap E2 asked for this: a locally-denied ICMP probe currently reaches the
 * panel as packet loss, indistinguishable from a target that does not answer.
 * The panel cannot fix that by itself — it is a property of the host, not of any
 * sample — so the node states the fact once and the panel shows it.
 *
 * The three states are deliberately not two. An empty value means **the agent has
 * not said**, which is what every node running a pre-`icmp_capability` build
 * reports. Rendering that as "unavailable" would paint working nodes as broken,
 * so it reads as "unknown" and says why.
 *
 * This component only reports. It does not hide tasks, drop samples or change
 * scheduling: silencing coverage is a separate decision with its own risks, and
 * `docs/ROADMAP.md` E2 keeps it out of this step.
 */
export type IcmpCapability = "raw" | "ping" | "none" | "" | undefined;

export function IcmpCapabilityBadge({
  capability,
  compact = false,
}: {
  capability: IcmpCapability;
  compact?: boolean;
}) {
  const { t } = useTranslation();

  const { icon, color, label, title } = describe(capability, compact, t);

  // `??` would keep an empty string, and an empty string is exactly what an agent
  // that has not reported sends. Normalising here keeps "unknown" a single value
  // for anything that reads the marker.
  const state = capability === "raw" || capability === "ping" || capability === "none"
    ? capability
    : "unknown";

  return (
    <Flex
      data-testid="icmp-capability"
      data-icmp-capability={state}
      align="center"
      gap="1"
      title={title}
      style={{ display: "inline-flex" }}
    >
      <span style={{ color: `var(--${color}-11)`, lineHeight: 0 }} aria-hidden="true">
        {icon}
      </span>
      {!compact && (
        <Text size="1" style={{ color: `var(--${color}-11)` }}>
          {label}
        </Text>
      )}
      <span className="sr-only">{title}</span>
    </Flex>
  );
}

function describe(
  capability: IcmpCapability,
  compact: boolean,
  t: (key: string, fallback: string) => string,
) {
  switch (capability) {
    case "raw":
    case "ping":
      return {
        icon: <ShieldCheck size={14} />,
        color: "green",
        label: "",
        title:
          capability === "raw"
            ? t(
                "admin.nodeDetail.icmp.raw",
                "ICMP 可用：裸 socket（节点以 root 或 CAP_NET_RAW 运行）",
              )
            : t(
                "admin.nodeDetail.icmp.ping",
                "ICMP 可用：走内核非特权 ping socket 回退",
              ),
      };
    case "none":
      return {
        icon: <AlertTriangle size={14} />,
        color: "amber",
        label: compact
          ? ""
          : t("admin.nodeDetail.icmp.noneLabel", "ICMP 不可用"),
        title: t(
          "admin.nodeDetail.icmp.none",
          "该节点无法发出 ICMP 探测：裸 socket 与非特权 ping socket 都打不开。" +
            "此节点上的 ICMP 任务显示的丢包是工具的限制，不是目标的行为。" +
            "修法：给 agent 二进制 cap_net_raw（setcap cap_net_raw+ep），" +
            "或让 net.ipv4.ping_group_range 覆盖该 agent 所在的用户组。",
        ),
      };
    default:
      return {
        icon: <CircleHelp size={14} />,
        color: "gray",
        label: compact ? "" : t("admin.nodeDetail.icmp.unknownLabel", "ICMP 未知"),
        title: t(
          "admin.nodeDetail.icmp.unknown",
          "该节点尚未上报 ICMP 能力（agent 版本早于 icmp_capability）。" +
            "未知不等于不可用：升级该节点 agent 后才会显示实际结果。",
        ),
      };
  }
}
