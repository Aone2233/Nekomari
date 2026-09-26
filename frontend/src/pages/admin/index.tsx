import React, { useEffect, useState } from "react";
import { useNodeDetails, type NodeDetail } from "@/contexts/NodeDetailsContext";
import { NodeDetailsProvider } from "@/contexts/NodeDetailsProvider";
import {
  Button,
  Callout,
  Checkbox,
  Dialog,
  Flex,
  IconButton,
  SegmentedControl,
  Text,
  TextArea,
  TextField,
} from "@radix-ui/themes";
import {
  CircleDollarSign,
  Copy,
  CornerRightUp,
  Pencil,
  Plus,
  Radar,
  Settings,
} from "lucide-react";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { formatBytes, stringToBytes } from "@/utils/unitHelper";
import Loading from "@/components/loading";
import Tips from "@/components/ui/tips";
import {
  SettingCardCollapse,
  SettingCardSelect,
  SettingCardShortTextInput,
  SettingCardSwitch,
} from "@/components/admin/SettingCard";
import { useSettings } from "@/lib/api";
import { SelectOrInput } from "@/components/ui/select-or-input";
import {
  quotePowerShellArg,
  quoteShellArg,
  quoteShellArgs,
} from "@/utils/shellQuote";
import { NodeTable } from "./nodeTable/NodeTable";
import { useIsSnapshotBackend } from "./nodeTable/useIsSnapshotBackend";
import type { Platform } from "./nodeTable/GenerateCommandButton";
import { requireClientMutationSuccess } from "./nodeTable/mutationResult";

const NodeDetailsPage = () => {
  return (
    <NodeDetailsProvider>
      <Layout />
    </NodeDetailsProvider>
  );
};

// Exported for the mounted regression in script/admin-node-table.browser.tsx.
// The page owns the state the fixture has to observe — the poll interval, the
// search filter, the sort, and the selection — and NodeTable owns only part of
// it, so mounting NodeTable alone would test the wrong wiring. Exporting the
// component is a visibility change: no behaviour depends on it.
export const Layout = () => {
  const { nodeDetail, isLoading, error, refresh } = useNodeDetails();
  const { settings, loading: settingsLoading } = useSettings();
  const [searchTerm, setSearchTerm] = useState("");
  const [selectedNodes, setSelectedNodes] = useState<string[]>([]);
  const filteredNodes = Array.isArray(nodeDetail)
    ? nodeDetail
        .filter((node) =>
          node.name.toLowerCase().includes(searchTerm.toLowerCase())
        )
        .sort((a, b) => a.weight - b.weight)
    : [];

  useEffect(() => {
    const interval = setInterval(() => { refresh() }, 5000);
    return () => clearInterval(interval);
    // 只依赖 refresh（NodeDetailsProvider 里已用 useCallback 固定引用）：
    // 之前依赖 nodeDetail 会让每次轮询响应都拆掉旧定时器、重建新定时器，
    // 请求频率没变但定时器被无限重建；现在每个挂载周期恰好一个 interval。
  }, [refresh]);

  if (isLoading) return <Loading text="" />;
  if (error) return <div>{error}</div>;

  const isEmpty = Array.isArray(nodeDetail) && nodeDetail.length === 0;

  return (
    <Flex direction="column" gap="4" p="4" className="km-page-admin-index">
      <Header
        searchTerm={searchTerm}
        setSearchTerm={setSearchTerm}
        selectedNodes={selectedNodes}
        settings={settings}
        settingsLoading={settingsLoading}
      />

      {isEmpty ? (
        <EmptyNodesGuide />
      ) : (
        <NodeTable
          nodes={filteredNodes}
          selectedNodes={selectedNodes}
          setSelectedNodes={setSelectedNodes}
          settings={settings}
        />
      )}
    </Flex>
  );
};

const EmptyNodesGuide = () => {
  const { t } = useTranslation();
  return (
    <Flex
      direction="column"
      align="end"
      justify="start"
      style={{ minHeight: "60vh" }}
      pr="2"
      pt="1"
    >
      {/* 回转箭头指向右上角的“添加节点”按钮 */}
      <CornerRightUp
        size={72}
        strokeWidth={1.25}
        className="text-[var(--accent-9)] animate-bounce"
        style={{ marginRight: "1.5rem" }}
      />
      <Flex direction="column" align="end" gap="1" mt="2" mr="2">
        <Text size="4" weight="bold">
          {t("admin.nodeTable.emptyGuide.title", "还没有任何服务器")}
        </Text>
        <Text size="2" color="gray" align="right" style={{ maxWidth: "20rem" }}>
          {t(
            "admin.nodeTable.emptyGuide.description",
            "点击右上角的“添加节点”开始，或开启自动发现批量接入服务器。"
          )}
        </Text>
      </Flex>
    </Flex>
  );
};

type AutoDiscoveryInstallOptions = {
  disableWebSsh: boolean;
  disableAutoUpdate: boolean;
  ignoreUnsafeCert: boolean;
  memoryIncludeCache: boolean;
  getIpAddrFromNic: boolean;
  enableGpu: boolean;
  ghproxy: string;
  dir: string;
  serviceName: string;
  includeNics: string;
  excludeNics: string;
  includeMountpoints: string;
  interval: string;
  monthRotate: string;
  installVersion: string;
};


const AutoDiscoverySection = ({
  settings,
  loading,
}: {
  settings: any;
  loading?: boolean;
}) => {
  const { t } = useTranslation();
  const adKey: string = settings?.auto_discovery_key || "";
  const enabled = Boolean(adKey);

  const [selectedPlatform, setSelectedPlatform] =
    React.useState<Platform>("linux");
  const [showOptions, setShowOptions] = React.useState(false);
  const [installOptions, setInstallOptions] =
    React.useState<AutoDiscoveryInstallOptions>({
      disableWebSsh: false,
      disableAutoUpdate: false,
      ignoreUnsafeCert: false,
      memoryIncludeCache: false,
      getIpAddrFromNic: false,
      enableGpu: false,
      ghproxy: "",
      dir: "",
      serviceName: "",
      includeNics: "",
      excludeNics: "",
      includeMountpoints: "",
      interval: "",
      monthRotate: "",
      installVersion: "",
    });

  const [enableGhproxy, setEnableGhproxy] = React.useState(false);
  const [enableCustomDir, setEnableCustomDir] = React.useState(false);
  const [enableCustomServiceName, setEnableCustomServiceName] =
    React.useState(false);
  const [enableIncludeNics, setEnableIncludeNics] = React.useState(false);
  const [enableExcludeNics, setEnableExcludeNics] = React.useState(false);
  const [enableIncludeMountpoints, setEnableIncludeMountpoints] =
    React.useState(false);
  const [enableInterval, setEnableInterval] = React.useState(false);
  const [enableMonthRotate, setEnableMonthRotate] = React.useState(false);
  const [enableInstallVersion, setEnableInstallVersion] = React.useState(false);
  const isSnapshotBackend = useIsSnapshotBackend();
  // 与原先 `useEffect(..., [showOptions, isSnapshotBackend])` 等价：任一依赖变化时
  // 在渲染期间按同一条件同步，快照后端下强制开启指定安装版本并回填默认值。
  const [syncedSnapshotDefaults, setSyncedSnapshotDefaults] = useState({
    showOptions,
    isSnapshotBackend,
  });
  if (
    syncedSnapshotDefaults.showOptions !== showOptions ||
    syncedSnapshotDefaults.isSnapshotBackend !== isSnapshotBackend
  ) {
    setSyncedSnapshotDefaults({ showOptions, isSnapshotBackend });
    if (showOptions && isSnapshotBackend) {
      setEnableInstallVersion(true);
      setInstallOptions((prev) => ({
        ...prev,
        installVersion: prev.installVersion.trim() || "snapshot",
      }));
    }
  }

  const generateCommand = () => {
    const host = (function () {
      if (!settings?.script_domain) {
        return window.location.origin;
      }
      if (settings.script_domain.startsWith("http")) {
        return settings.script_domain.replace(/\/+$/, "");
      }
      return `http://${settings.script_domain.replace(/\/+$/, "")}`;
    })();
    const args: string[] = ["-e", host, "--auto-discovery", adKey];
    if (installOptions.disableWebSsh) {
      args.push("--disable-web-ssh");
    }
    if (installOptions.disableAutoUpdate) {
      args.push("--disable-auto-update");
    }
    if (installOptions.ignoreUnsafeCert) {
      args.push("--ignore-unsafe-cert");
    }
    if (installOptions.memoryIncludeCache) {
      args.push("--memory-include-cache");
    }
    if (installOptions.getIpAddrFromNic) {
      args.push("--get-ip-addr-from-nic");
    }
    if (installOptions.enableGpu) {
      args.push("--gpu");
    }
    const ghproxy = installOptions.ghproxy.trim();
    if (enableGhproxy && ghproxy) {
      const finalUrl = (
        ghproxy.startsWith("http") ? ghproxy : `http://${ghproxy}`
      ).replace(/\/+$/, "");
      args.push(`--install-ghproxy`);
      args.push(finalUrl);
    }
    const installDir = installOptions.dir.trim();
    if (enableCustomDir && installDir) {
      args.push(`--install-dir`);
      args.push(installDir);
    }
    const serviceName = installOptions.serviceName.trim();
    if (enableCustomServiceName && serviceName) {
      args.push(`--install-service-name`);
      args.push(serviceName);
    }
    const installVersion = installOptions.installVersion.trim();
    if (enableInstallVersion && installVersion) {
      args.push(`--version`);
      args.push(installVersion);
    }
    const includeNics = installOptions.includeNics.trim();
    if (enableIncludeNics && includeNics) {
      args.push(`--include-nics`);
      args.push(includeNics);
    }
    const excludeNics = installOptions.excludeNics.trim();
    if (enableExcludeNics && excludeNics) {
      args.push(`--exclude-nics`);
      args.push(excludeNics);
    }
    const includeMountpoints = installOptions.includeMountpoints.trim();
    if (enableIncludeMountpoints && includeMountpoints) {
      args.push(`--include-mountpoint`);
      args.push(includeMountpoints);
    }
    if (enableInterval) {
      const intervalVal = Number.parseFloat(
        (installOptions.interval || "").trim()
      );
      args.push("-i");
      args.push(
        Number.isFinite(intervalVal) && intervalVal >= 1
          ? String(intervalVal)
          : "1"
      );
    }
    if (enableMonthRotate) {
      const rotateVal = (installOptions.monthRotate || "").trim() || "1";
      args.push(`--month-rotate`);
      args.push(rotateVal);
    }

    // These point at THIS repository's installers, not upstream's.
    //
    // They used to reference komari-monitor/komari-agent, which installs the
    // upstream agent from an archived project -- without the flags this fork added
    // (--month-rotate, --disable-web-ssh, and the rest). Anyone copying the command
    // out of the panel got a differently-shaped node: upstream's installer uses
    // /opt/komari, a komari-agent unit and --auto-discovery, while this repository's
    // installs /opt/nekomari-agent with the node's own token.
    // components/admin/NodeTable/NodeFunction.tsx was migrated; these two
    // generators in this file were missed.
    let scriptUrl =
      selectedPlatform === "windows"
        ? "https://raw.githubusercontent.com/Aone2233/Nekomari/main/deploy/install-node-agent.ps1"
        : "https://raw.githubusercontent.com/Aone2233/Nekomari/main/deploy/install-node-agent.sh";
    if (enableGhproxy && ghproxy) {
      scriptUrl = scriptUrl.slice(8); // 去掉 https://
      if (ghproxy.endsWith("/")) {
        scriptUrl = `${ghproxy}${scriptUrl}`;
      } else {
        scriptUrl = `${ghproxy}/${scriptUrl}`;
      }
      if (!scriptUrl.startsWith("http")) {
        scriptUrl = `http://${scriptUrl}`;
      }
    }

    let finalCommand = "";
    switch (selectedPlatform) {
      case "linux":
        finalCommand =
          `wget -qO- ${quoteShellArg(scriptUrl)} | sudo bash -s -- ` +
          quoteShellArgs(args);
        break;
      case "windows":
        finalCommand =
          `powershell.exe -NoProfile -ExecutionPolicy Bypass -Command ` +
          `"iwr ${quotePowerShellArg(scriptUrl)}` +
          ` -UseBasicParsing -OutFile 'install.ps1'; &` +
          ` '.\\install.ps1'`;
        args.forEach((arg) => {
          finalCommand += ` ${quotePowerShellArg(arg)}`;
        });
        finalCommand += `"`;
        break;
      case "macos":
        finalCommand =
          `curl -fsSL ${quoteShellArg(scriptUrl)} | sudo bash -s -- ` +
          quoteShellArgs(args);
        break;
      case "docker": {
        // Docker 运行时不支持安装脚本专用参数，剔除它们及其取值
        const installOnlyFlags = [
          "--install-ghproxy",
          "--install-dir",
          "--install-service-name",
          "--version",
        ];
        const dockerArgs: string[] = [];
        for (let i = 0; i < args.length; i++) {
          if (installOnlyFlags.includes(args[i])) {
            i++; // 跳过该标志的取值
            continue;
          }
          dockerArgs.push(args[i]);
        }
        // 镜像指向本仓库发布的探针镜像（docker.yml 从 Release 附件构建），不再是
        // 上游的 komari-monitor/komari-agent —— 那是另一个项目、没有本 fork 的 flag。
        //
        // 命令可重复执行：先删掉同名容器，再用 --pull always 取最新镜像，所以
        // 「更新探针」就是再跑一次这条命令。
        //
        // /data 是探针的工作目录：net_static.json（--month-rotate 的流量账本）和
        // auto-discovery.json（自动发现注册下来的身份）都写在这里，必须挂卷持久化。
        // 少了它，容器重建后流量窗口为空（面板少算）、身份丢失（会注册出重复节点）。
        // 上游那条命令用 bind mount 单文件来保身份，要求宿主机上文件已存在，
        // 否则 Docker 会把它建成目录；命名卷没有这个坑。
        if (!dockerArgs.includes("--disable-auto-update")) {
          // 容器不能自我更新：agent 的自更新会去替换自己的二进制，而容器的更新
          // 方式是换镜像。这里强制关闭，更新走重跑命令。
          dockerArgs.push("--disable-auto-update");
        }
        finalCommand =
          `docker rm -f komari-agent >/dev/null 2>&1; ` +
          `docker run -d --name komari-agent --restart=always --pull always ` +
          `-v komari-agent-data:/data ` +
          `ghcr.io/aone2233/nekomari-agent:latest ` +
          quoteShellArgs(dockerArgs);
        break;
      }
    }
    return finalCommand;
  };

  const copyToClipboard = async (text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      toast.success(t("copy_success", "已复制到剪贴板"));
    } catch (err) {
      console.error("Failed to copy text: ", err);
    }
  };

  if (loading) {
    return (
      <Flex align="center" justify="center" mt="4" py="4">
        <Loading text="" />
      </Flex>
    );
  }

  if (!enabled) {
    return (
      <Callout.Root color="blue" mt="4" size="1">
        <Callout.Icon>
          <Radar size={16} />
        </Callout.Icon>
        <Callout.Text>
          <Flex direction="column" gap="2" align="start">
            <Text weight="bold">
              {t("admin.nodeTable.autoDiscovery.tryIt", "试试自动发现")}
            </Text>
            <Text size="2">
              {t(
                "admin.nodeTable.autoDiscovery.disabledDescription",
                "开启自动发现后，无需逐台手动添加节点。只要在目标服务器上运行一条命令，Agent 就会携带密钥自动注册并上线，非常适合批量部署多台服务器。"
              )}
            </Text>
            <Link to="/admin/settings/general">
              <Button variant="soft" size="1">
                <Settings size={14} />
                {t(
                  "admin.nodeTable.autoDiscovery.goToSettings",
                  "前往“常规设置”开启自动发现"
                )}
              </Button>
            </Link>
          </Flex>
        </Callout.Text>
      </Callout.Root>
    );
  }

  return (
    <Flex direction="column" gap="3" mt="4">
      <Flex direction="column" gap="1">
        <Flex gap="2" align="center">
          <Radar size={16} />
          <Text weight="bold">
            {t("admin.nodeTable.autoDiscovery.title", "自动发现")}
          </Text>
        </Flex>
        <Text size="2" color="gray">
          {t(
            "admin.nodeTable.autoDiscovery.enabledDescription",
            "在目标服务器上运行下面的命令，Agent 将自动注册并上线，无需手动添加节点。"
          )}
        </Text>
      </Flex>

      <SegmentedControl.Root
        value={selectedPlatform}
        onValueChange={(value) => setSelectedPlatform(value as Platform)}
      >
        <SegmentedControl.Item value="linux">Linux</SegmentedControl.Item>
        <SegmentedControl.Item value="windows">Windows</SegmentedControl.Item>
        <SegmentedControl.Item value="macos">macOS</SegmentedControl.Item>
        <SegmentedControl.Item value="docker">Docker</SegmentedControl.Item>
      </SegmentedControl.Root>

      <Flex gap="2" align="center">
        <Checkbox
          checked={showOptions}
          onCheckedChange={(checked) => setShowOptions(Boolean(checked))}
        />
        <label
          className="text-sm font-bold cursor-pointer"
          onClick={() => setShowOptions((prev) => !prev)}
        >
          {t("admin.nodeTable.installOptions", "安装选项")}
        </label>
      </Flex>

      {showOptions && (
        <Flex direction="column" gap="2">
          <div className="grid grid-cols-2 gap-2">
            <Flex gap="2" align="center">
              <Checkbox
                checked={installOptions.disableWebSsh}
                onCheckedChange={(checked) =>
                  setInstallOptions((prev) => ({
                    ...prev,
                    disableWebSsh: Boolean(checked),
                  }))
                }
              />
              <label
                className="text-sm font-normal cursor-pointer"
                onClick={() =>
                  setInstallOptions((prev) => ({
                    ...prev,
                    disableWebSsh: !prev.disableWebSsh,
                  }))
                }
              >
                {t("admin.nodeTable.disableWebSsh")}
              </label>
            </Flex>
            <Flex gap="2" align="center">
              <Checkbox
                checked={installOptions.disableAutoUpdate}
                onCheckedChange={(checked) =>
                  setInstallOptions((prev) => ({
                    ...prev,
                    disableAutoUpdate: Boolean(checked),
                  }))
                }
              />
              <label
                className="text-sm font-normal cursor-pointer"
                onClick={() =>
                  setInstallOptions((prev) => ({
                    ...prev,
                    disableAutoUpdate: !prev.disableAutoUpdate,
                  }))
                }
              >
                {t("admin.nodeTable.disableAutoUpdate", "禁用自动更新")}
              </label>
            </Flex>
            <Flex gap="2" align="center">
              <Checkbox
                checked={installOptions.ignoreUnsafeCert}
                onCheckedChange={(checked) =>
                  setInstallOptions((prev) => ({
                    ...prev,
                    ignoreUnsafeCert: Boolean(checked),
                  }))
                }
              />
              <label
                className="text-sm font-normal cursor-pointer"
                onClick={() =>
                  setInstallOptions((prev) => ({
                    ...prev,
                    ignoreUnsafeCert: !prev.ignoreUnsafeCert,
                  }))
                }
              >
                {t("admin.nodeTable.ignoreUnsafeCert", "忽略不安全证书")}
              </label>
            </Flex>
            <Flex gap="2" align="center">
              <Checkbox
                checked={installOptions.memoryIncludeCache}
                onCheckedChange={(checked) =>
                  setInstallOptions((prev) => ({
                    ...prev,
                    memoryIncludeCache: Boolean(checked),
                  }))
                }
              />
              <label
                className="text-sm font-normal cursor-pointer"
                onClick={() =>
                  setInstallOptions((prev) => ({
                    ...prev,
                    memoryIncludeCache: !prev.memoryIncludeCache,
                  }))
                }
              >
                {t("admin.nodeTable.memoryModeAvailable", "监测可用内存")}
              </label>
              <Tips size="14">
                {t("admin.nodeTable.memoryModeAvailable_tip")}
              </Tips>
            </Flex>
            <Flex gap="2" align="center">
              <Checkbox
                checked={installOptions.getIpAddrFromNic}
                onCheckedChange={(checked) =>
                  setInstallOptions((prev) => ({
                    ...prev,
                    getIpAddrFromNic: Boolean(checked),
                  }))
                }
              />
              <label
                className="text-sm font-normal cursor-pointer"
                onClick={() =>
                  setInstallOptions((prev) => ({
                    ...prev,
                    getIpAddrFromNic: !prev.getIpAddrFromNic,
                  }))
                }
              >
                {t("admin.nodeTable.getIpAddrFromNic", "从网卡获取 IP 地址")}
              </label>
            </Flex>
            <Flex gap="2" align="center">
              <Checkbox
                checked={installOptions.enableGpu}
                onCheckedChange={(checked) =>
                  setInstallOptions((prev) => ({
                    ...prev,
                    enableGpu: Boolean(checked),
                  }))
                }
              />
              <label
                className="text-sm font-normal cursor-pointer"
                onClick={() =>
                  setInstallOptions((prev) => ({
                    ...prev,
                    enableGpu: !prev.enableGpu,
                  }))
                }
              >
                {t("admin.nodeTable.enableGpuMonitoring", "启用详细 GPU 监控")}
              </label>
            </Flex>
          </div>

          <Flex direction="column" gap="2">
            <Flex gap="2" align="center">
              <Checkbox
                checked={enableInstallVersion}
                onCheckedChange={(checked) => {
                  setEnableInstallVersion(Boolean(checked));
                  if (!checked) {
                    setInstallOptions((prev) => ({
                      ...prev,
                      installVersion: "",
                    }));
                  }
                }}
              />
              <label
                className="text-sm font-bold cursor-pointer"
                onClick={() => {
                  const willEnable = !enableInstallVersion;
                  setEnableInstallVersion(willEnable);
                  if (!willEnable) {
                    setInstallOptions((prev) => ({
                      ...prev,
                      installVersion: "",
                    }));
                  }
                }}
              >
                {t("admin.nodeTable.installVersion", "指定安装版本")}
              </label>
            </Flex>
            {enableInstallVersion && (
              <TextField.Root
                placeholder="snapshot"
                value={installOptions.installVersion}
                onChange={(e) =>
                  setInstallOptions((prev) => ({
                    ...prev,
                    installVersion: e.target.value,
                  }))
                }
              />
            )}

            <Flex gap="2" align="center">
              <Checkbox
                checked={enableGhproxy}
                onCheckedChange={(checked) => {
                  setEnableGhproxy(Boolean(checked));
                  if (!checked) {
                    setInstallOptions((prev) => ({ ...prev, ghproxy: "" }));
                  }
                }}
              />
              <label
                className="text-sm font-bold cursor-pointer"
                onClick={() => {
                  setEnableGhproxy(!enableGhproxy);
                  if (enableGhproxy) {
                    setInstallOptions((prev) => ({ ...prev, ghproxy: "" }));
                  }
                }}
              >
                {t("admin.nodeTable.ghproxy", "GitHub 代理")}
              </label>
            </Flex>
            {enableGhproxy && (
              <TextField.Root
                placeholder="https://ghfast.top/"
                value={installOptions.ghproxy}
                onChange={(e) =>
                  setInstallOptions((prev) => ({
                    ...prev,
                    ghproxy: e.target.value,
                  }))
                }
              />
            )}

            <Flex gap="2" align="center">
              <Checkbox
                checked={enableCustomDir}
                onCheckedChange={(checked) => {
                  setEnableCustomDir(Boolean(checked));
                  if (!checked) {
                    setInstallOptions((prev) => ({ ...prev, dir: "" }));
                  }
                }}
              />
              <label
                className="text-sm font-bold cursor-pointer"
                onClick={() => {
                  setEnableCustomDir(!enableCustomDir);
                  if (enableCustomDir) {
                    setInstallOptions((prev) => ({ ...prev, dir: "" }));
                  }
                }}
              >
                {t("admin.nodeTable.install_dir", "安装目录")}
              </label>
            </Flex>
            {enableCustomDir && (
              <TextField.Root
                placeholder={t(
                  "admin.nodeTable.install_dir_placeholder",
                  "安装目录，为空则使用默认目录(/opt/komari-agent)"
                )}
                value={installOptions.dir}
                onChange={(e) =>
                  setInstallOptions((prev) => ({
                    ...prev,
                    dir: e.target.value,
                  }))
                }
              />
            )}

            <Flex gap="2" align="center">
              <Checkbox
                checked={enableCustomServiceName}
                onCheckedChange={(checked) => {
                  setEnableCustomServiceName(Boolean(checked));
                  if (!checked) {
                    setInstallOptions((prev) => ({ ...prev, serviceName: "" }));
                  }
                }}
              />
              <label
                className="text-sm font-bold cursor-pointer"
                onClick={() => {
                  setEnableCustomServiceName(!enableCustomServiceName);
                  if (enableCustomServiceName) {
                    setInstallOptions((prev) => ({ ...prev, serviceName: "" }));
                  }
                }}
              >
                {t("admin.nodeTable.serviceName", "服务名称")}
              </label>
            </Flex>
            {enableCustomServiceName && (
              <TextField.Root
                placeholder={t(
                  "admin.nodeTable.serviceName_placeholder",
                  "服务名称，为空则使用默认名称(komari-agent)"
                )}
                value={installOptions.serviceName}
                onChange={(e) =>
                  setInstallOptions((prev) => ({
                    ...prev,
                    serviceName: e.target.value,
                  }))
                }
              />
            )}

            <Flex gap="2" align="center">
              <Checkbox
                checked={enableIncludeNics}
                onCheckedChange={(checked) => {
                  setEnableIncludeNics(Boolean(checked));
                  if (!checked) {
                    setInstallOptions((prev) => ({ ...prev, includeNics: "" }));
                  }
                }}
              />
              <label
                className="text-sm font-bold cursor-pointer"
                onClick={() => {
                  setEnableIncludeNics(!enableIncludeNics);
                  if (enableIncludeNics) {
                    setInstallOptions((prev) => ({ ...prev, includeNics: "" }));
                  }
                }}
              >
                {t("admin.nodeTable.includeNics", "只监测特定网卡")}
              </label>
            </Flex>
            {enableIncludeNics && (
              <TextField.Root
                placeholder="eth0,eth1"
                value={installOptions.includeNics}
                onChange={(e) =>
                  setInstallOptions((prev) => ({
                    ...prev,
                    includeNics: e.target.value,
                  }))
                }
              />
            )}

            <Flex gap="2" align="center">
              <Checkbox
                checked={enableExcludeNics}
                onCheckedChange={(checked) => {
                  setEnableExcludeNics(Boolean(checked));
                  if (!checked) {
                    setInstallOptions((prev) => ({ ...prev, excludeNics: "" }));
                  }
                }}
              />
              <label
                className="text-sm font-bold cursor-pointer"
                onClick={() => {
                  setEnableExcludeNics(!enableExcludeNics);
                  if (enableExcludeNics) {
                    setInstallOptions((prev) => ({ ...prev, excludeNics: "" }));
                  }
                }}
              >
                {t("admin.nodeTable.excludeNics", "排除特定网卡")}
              </label>
            </Flex>
            {enableExcludeNics && (
              <TextField.Root
                placeholder="lo"
                value={installOptions.excludeNics}
                onChange={(e) =>
                  setInstallOptions((prev) => ({
                    ...prev,
                    excludeNics: e.target.value,
                  }))
                }
              />
            )}

            <Flex gap="2" align="center">
              <Checkbox
                checked={enableIncludeMountpoints}
                onCheckedChange={(checked) => {
                  setEnableIncludeMountpoints(Boolean(checked));
                  if (!checked) {
                    setInstallOptions((prev) => ({
                      ...prev,
                      includeMountpoints: "",
                    }));
                  }
                }}
              />
              <label
                className="text-sm font-bold cursor-pointer"
                onClick={() => {
                  setEnableIncludeMountpoints(!enableIncludeMountpoints);
                  if (enableIncludeMountpoints) {
                    setInstallOptions((prev) => ({
                      ...prev,
                      includeMountpoints: "",
                    }));
                  }
                }}
              >
                {t("admin.nodeTable.includeMountpoints", "只监测特定挂载点")}
              </label>
            </Flex>
            {enableIncludeMountpoints && (
              <TextField.Root
                placeholder="/;/home;/var"
                value={installOptions.includeMountpoints}
                onChange={(e) =>
                  setInstallOptions((prev) => ({
                    ...prev,
                    includeMountpoints: e.target.value,
                  }))
                }
              />
            )}

            <Flex gap="2" align="center">
              <Checkbox
                checked={enableInterval}
                onCheckedChange={(checked) => {
                  const en = Boolean(checked);
                  setEnableInterval(en);
                  setInstallOptions((prev) => ({
                    ...prev,
                    interval: en
                      ? prev.interval?.trim()
                        ? prev.interval
                        : "1"
                      : "",
                  }));
                }}
              />
              <label
                className="text-sm font-bold cursor-pointer"
                onClick={() => {
                  const willEnable = !enableInterval;
                  setEnableInterval(willEnable);
                  setInstallOptions((prev) => ({
                    ...prev,
                    interval: willEnable
                      ? prev.interval?.trim()
                        ? prev.interval
                        : "1"
                      : "",
                  }));
                }}
              >
                {t("admin.nodeTable.interval", "采集间隔(秒)")}
              </label>
            </Flex>
            {enableInterval && (
              <TextField.Root
                placeholder="1"
                type="number"
                min="1"
                step="0.1"
                value={installOptions.interval}
                onChange={(e) =>
                  setInstallOptions((prev) => ({
                    ...prev,
                    interval: e.target.value,
                  }))
                }
              />
            )}

            <Flex gap="2" align="center">
              <Checkbox
                checked={enableMonthRotate}
                onCheckedChange={(checked) => {
                  const en = Boolean(checked);
                  setEnableMonthRotate(en);
                  setInstallOptions((prev) => ({
                    ...prev,
                    monthRotate: en
                      ? prev.monthRotate?.trim()
                        ? prev.monthRotate
                        : "1"
                      : "",
                  }));
                }}
              />
              <label
                className="text-sm font-bold cursor-pointer"
                onClick={() => {
                  const willEnable = !enableMonthRotate;
                  setEnableMonthRotate(willEnable);
                  setInstallOptions((prev) => ({
                    ...prev,
                    monthRotate: willEnable
                      ? prev.monthRotate?.trim()
                        ? prev.monthRotate
                        : "1"
                      : "",
                  }));
                }}
              >
                {t("admin.nodeTable.monthRotate", "网络统计月重置")}
              </label>
            </Flex>
            {enableMonthRotate && (
              <TextField.Root
                placeholder="1"
                type="number"
                min="1"
                max="31"
                value={installOptions.monthRotate}
                onChange={(e) =>
                  setInstallOptions((prev) => ({
                    ...prev,
                    monthRotate: e.target.value,
                  }))
                }
              />
            )}
          </Flex>
        </Flex>
      )}

      <Flex direction="column" gap="2">
        <label className="text-sm font-bold">
          {t("admin.nodeTable.generatedCommand", "指令")}
        </label>
        <TextArea
          disabled
          className="w-full"
          style={{ minHeight: "80px" }}
          value={generateCommand()}
        />
      </Flex>
      <Button
        style={{ width: "100%" }}
        onClick={() => copyToClipboard(generateCommand())}
      >
        <Copy size={16} />
        {t("common.copy")}
      </Button>
    </Flex>
  );
};

const Header = ({
  searchTerm,
  setSearchTerm,
  selectedNodes,
  settings,
  settingsLoading,
}: {
  searchTerm: string;
  setSearchTerm: (term: string) => void;
  selectedNodes: string[];
  settings: any;
  settingsLoading: boolean;
}) => {
  const { t } = useTranslation();
  const { refresh } = useNodeDetails();
  const [loading, setLoading] = useState(false);
  const [dialogOpen, setDialogOpen] = useState(false);
  const inputRef = React.useRef<HTMLInputElement>(null);
  const handleAddNode = async (name: string | undefined) => {
    setLoading(true);
    try {
      const response = await fetch("/api/admin/client/add", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: name || "" }),
      });
      await requireClientMutationSuccess(response);
      refresh();
      setDialogOpen(false);
    } catch (error) {
      toast.error(
        `${t("common.error", "Error")}: ${
          error instanceof Error ? error.message : String(error)
        }`
      );
    } finally {
      setLoading(false);
    }
  };
  return (
    <Flex justify="between" align="center" gap="4" wrap="wrap">
      <Flex gap="2" align="center">
        <Text size="5" weight="bold">
          {t("admin.nodeTable.nodeList")}
        </Text>
        {selectedNodes.length > 0 && (
          <Text size="2" data-testid="selected-count">
            ({selectedNodes.length} selected)
          </Text>
        )}
      </Flex>
      <Flex gap="2">
        <TextField.Root
          placeholder={t("admin.nodeTable.searchByName")}
          value={searchTerm}
          onChange={(e) => setSearchTerm(e.target.value)}
        />
        <Dialog.Root open={dialogOpen} onOpenChange={setDialogOpen}>
          <Dialog.Trigger>
            <Button onClick={() => setDialogOpen(true)}>
              <Plus size={16} />
              {t("admin.nodeTable.addNode")}
            </Button>
          </Dialog.Trigger>
          <Dialog.Content>
            <Dialog.Title>{t("admin.nodeTable.addNode")}</Dialog.Title>
            <TextField.Root
              ref={inputRef}
              placeholder={t("admin.nodeTable.nameOptional")}
            />
            <Flex justify="end" gap="2" mt="4">
              <Button
                onClick={() => handleAddNode(inputRef.current?.value)}
                disabled={loading}
              >
                {t("admin.nodeTable.addNode")}
              </Button>
            </Flex>
            <AutoDiscoverySection
              settings={settings}
              loading={settingsLoading}
            />
          </Dialog.Content>
        </Dialog.Root>
      </Flex>
    </Flex>
  );
};




export default NodeDetailsPage;

export function EditButton({ node }: { node: NodeDetail }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const { refresh } = useNodeDetails();
  const nameRef = React.useRef<HTMLInputElement>(null);
  const groupRef = React.useRef<HTMLInputElement>(null);
  const tagsRef = React.useRef<HTMLInputElement>(null);
  const publicRemarkRef = React.useRef<HTMLTextAreaElement>(null);
  const privateRemarkRef = React.useRef<HTMLTextAreaElement>(null);
  const [hidden, setHidden] = useState(false);
  const [saving, setSaving] = useState(false);
  const [traffic_limit, setTrafficLimit] = useState(0);
  const [traffic_limit_type, setTrafficLimitType] = useState("sum");
  // 编辑态由 node 派生：node 的对应字段变化时在渲染期间同步一次，
  // 避免在 effect 中同步 setState（同一渲染批次内完成，不再多画一帧旧值）。
  // 哨兵必须从 null 起步：下面三个 state 的初值是 false/0/"sum" 而非 node 的字段值，
  // 所以原 effect 在挂载时那次运行是有实际写入的，不能被跳过。
  const [syncedNode, setSyncedNode] = useState<{
    hidden: boolean;
    traffic_limit: number;
    traffic_limit_type: string;
  } | null>(null);

  if (
    syncedNode === null ||
    syncedNode.hidden !== node.hidden ||
    syncedNode.traffic_limit !== node.traffic_limit ||
    syncedNode.traffic_limit_type !== node.traffic_limit_type
  ) {
    setSyncedNode({
      hidden: node.hidden,
      traffic_limit: node.traffic_limit,
      traffic_limit_type: node.traffic_limit_type,
    });
    setHidden(node.hidden);
    setTrafficLimit(node.traffic_limit || 0);
    setTrafficLimitType(node.traffic_limit_type || "sum");
  }

  const save = async () => {
    try {
      setSaving(true);
      const response = await fetch(`/api/admin/client/${node.uuid}/edit`, {
        method: "POST",
        body: JSON.stringify({
          name: nameRef.current?.value,
          remark: privateRemarkRef.current?.value,
          public_remark: publicRemarkRef.current?.value,
          group: groupRef.current?.value,
          tags: tagsRef.current?.value,
          hidden,
          traffic_limit,
          traffic_limit_type,
        }),
        headers: {
          "Content-Type": "application/json",
        },
      });
      await requireClientMutationSuccess(response);
      refresh();
      setOpen(false);
      toast.success(t("admin.nodeEdit.saveSuccess", "保存成功"));
    } catch (error) {
      toast.error(
        `${t("common.error", "Error")}: ${error instanceof Error ? error.message : String(error)}`
      );
    } finally {
      setSaving(false);
    }
  };
  return (
    <Dialog.Root open={open} onOpenChange={setOpen}>
      <Dialog.Trigger>
        <IconButton
          variant="ghost"
          title={t("admin.nodeEdit.editInfo", "编辑信息")}
        >
          <Pencil size="18" />
        </IconButton>
      </Dialog.Trigger>
      <Dialog.Content>
        <Dialog.Title>{t("admin.nodeEdit.editInfo", "编辑信息")}</Dialog.Title>
        <div className="flex flex-col gap-4">
          <div>
            <label className="block mb-1 text-sm font-medium text-muted-foreground">
              {t("admin.nodeEdit.name", "名称")}
            </label>
            <TextField.Root
              defaultValue={node.name}
              placeholder={t("admin.nodeEdit.namePlaceholder", "请输入名称")}
              ref={nameRef}
            />
          </div>
          <div>
            <label className="block mb-1 text-sm font-medium text-muted-foreground">
              {t("admin.nodeEdit.token", "Token 令牌")}
            </label>
            <TextField.Root
              value={node.token}
              placeholder={t("admin.nodeEdit.tokenPlaceholder", "请输入 Token")}
              readOnly
            />
          </div>
          <div>
            <label className="mb-1 text-sm font-medium text-muted-foreground flex items-center">
              {t("common.tags")}
              <label className="text-muted-foreground ml-1 text-xs self-end">
                {t("common.tagsDescription")}
              </label>
              <Tips>
                <span
                  dangerouslySetInnerHTML={{ __html: t("common.tagsTips") }}
                />
              </Tips>
            </label>
            <TextField.Root defaultValue={node.tags} ref={tagsRef} />
          </div>
          <div>
            <label className="block mb-1 text-sm font-medium text-muted-foreground">
              {t("common.group")}
            </label>
            <TextField.Root defaultValue={node.group} ref={groupRef} />
          </div>
          <div>
            <label className="block mb-1 text-sm font-medium text-muted-foreground">
              {t("admin.nodeEdit.remark", "私有备注")}
            </label>
            <TextArea
              defaultValue={node.remark}
              ref={privateRemarkRef}
              resize={"vertical"}
              placeholder={t(
                "admin.nodeEdit.remarkPlaceholder",
                "请输入私有备注"
              )}
            />
          </div>
          <div>
            <label className="block mb-1 text-sm font-medium text-muted-foreground">
              {t("admin.nodeEdit.publicRemark", "公开备注")}
            </label>
            <TextArea
              defaultValue={node.public_remark}
              resize={"vertical"}
              placeholder={t(
                "admin.nodeEdit.publicRemarkPlaceholder",
                "请输入公开备注"
              )}
              ref={publicRemarkRef}
            />
          </div>
          <div>
            <SettingCardSwitch
              title={t("admin.nodeEdit.hidden")}
              description={t("admin.nodeEdit.hidden_description")}
              defaultChecked={hidden}
              onChange={setHidden}
            />
          </div>
          <SettingCardCollapse title={t("admin.nodeEdit.trafficLimit")}>
            <SettingCardSelect
              bordless
              title={t("admin.nodeEdit.trafficLimitType")}
              defaultValue={node.traffic_limit_type || "max"}
              options={[
                {
                  label: t("admin.nodeEdit.trafficLimitType_sum"),
                  value: "sum",
                },
                {
                  label: t("admin.nodeEdit.trafficLimitType_max"),
                  value: "max",
                },
                {
                  label: t("admin.nodeEdit.trafficLimitType_min"),
                  value: "min",
                },
                {
                  label: t("admin.nodeEdit.trafficLimitType_up"),
                  value: "up",
                },
                {
                  label: t("admin.nodeEdit.trafficLimitType_down"),
                  value: "down",
                },
              ]}
              OnSave={(value) => {
                setTrafficLimitType(value);
              }}
            />
            <SettingCardShortTextInput
              bordless
              title={t("admin.nodeEdit.trafficLimit")}
              description={t("admin.nodeEdit.trafficLimit_description")}
              defaultValue={formatBytes(traffic_limit || 0)}
              showSaveButton={false}
              onChange={(e) => {
                setTrafficLimit(stringToBytes(e.currentTarget.value));
              }}
              onBlur={(e) => {
                e.currentTarget.value = formatBytes(traffic_limit);
              }}
            ></SettingCardShortTextInput>
          </SettingCardCollapse>
        </div>
        <Flex gap="2" justify={"end"} className="mt-4">
          <Button
            type="submit"
            className="w-full"
            disabled={saving}
            onClick={save}
          >
            {saving
              ? t("admin.nodeEdit.waiting", "等待...")
              : t("common.save", "保存")}
          </Button>
        </Flex>
      </Dialog.Content>
    </Dialog.Root>
  );
}


export function BillingButton({ node }: { node: NodeDetail }) {
  const { t } = useTranslation();
  const { refresh } = useNodeDetails();
  const [open, setOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [billingCycle, setBillingCycle] = React.useState<string>(
    node.billing_cycle.toString()
  );
  const [autoRenewal, setAutoRenewal] = React.useState<boolean>(
    node.auto_renewal || false
  );
  const [currency, setCurrency] = React.useState<string>(node.currency || "$");

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      setSaving(true);
      const formData = new FormData(e.target as HTMLFormElement);
      const priceValue = (formData.get("price") as string) || "0";

      const price = parseFloat(priceValue);

      if (isNaN(price) || (price < 0 && price !== -1)) {
        toast.error(t("admin.nodeTable.invalidPrice"));
        return;
      }
      const billingCycleValue = parseInt(
        (formData.get("billingCycle") as string) || "30"
      );
      const expiredAtValue = (formData.get("expiredAt") as string) || "";
      const expiredAt = expiredAtValue
        ? new Date(`${expiredAtValue}T00:00:00Z`).toISOString()
        : null;
      const currencyValue = (formData.get("currency") as string) || "$";

      const response = await fetch(`/api/admin/client/${node.uuid}/edit`, {
        method: "POST",
        body: JSON.stringify({
          price,
          billing_cycle: billingCycleValue,
          expired_at: expiredAt,
          currency: currencyValue,
          auto_renewal: autoRenewal,
        }),
        headers: {
          "Content-Type": "application/json",
        },
      });
      await requireClientMutationSuccess(response);
      refresh();
      setOpen(false);
    } catch (error) {
      toast.error("Failed to save billing information:" + error);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog.Root open={open} onOpenChange={setOpen}>
      <Dialog.Trigger>
        <IconButton
          variant="ghost"
          title={t("admin.nodeTable.billing", "账单")}
        >
          <CircleDollarSign size="18" />
        </IconButton>
      </Dialog.Trigger>
      <Dialog.Content>
        <Dialog.Title>{t("admin.nodeTable.billing", "账单")}</Dialog.Title>
        <form onSubmit={handleSave}>
          <Flex direction="column" gap="2">
            <label className="font-bold">
              <label>{t("admin.nodeTable.price")}</label>
              <label className="text-muted-foreground text-sm ml-1 font-medium">
                {t("admin.nodeTable.priceTips")}
              </label>
            </label>
            <TextField.Root name="price" defaultValue={node.price} />

            <label className="font-bold">
              <label>{t("admin.nodeTable.currency", "货币")}</label>
              <label className="text-muted-foreground text-sm ml-1 font-medium">
                {t("admin.nodeTable.currencyTips")}
              </label>
            </label>
            <TextField.Root
              name="currency"
              defaultValue={currency}
              onChange={(e) => setCurrency(e.target.value)}
            />

            <label className="font-bold flex items-center gap-1">
              {t("admin.nodeTable.billingCycle")} <Tips><span dangerouslySetInnerHTML={{ __html: t("admin.nodeTable.billingCycleTips") }}></span></Tips>
            </label>
            <SelectOrInput
            options={[
              { label: t("common.monthly"), value: "30" },
              { label: t("common.quarterly"), value: "92" },
              { label: t("common.semi_annual"), value: "184" },
              { label: t("common.annual"), value: "365" },
              { label: t("common.biennial"), value: "730" },
              { label: t("common.triennial"), value: "1095" },
              { label: t("common.quinquennial"), value: "1825" },
              { label: t("common.once"), value: "-1" },
            ]}
            type="number"
            name="billingCycle"
            value={billingCycle === "0" ? "" : billingCycle}
            onChange={setBillingCycle}
          />

            <Flex gap="2" align="center">
              <label className="font-bold">
                {t("admin.nodeTable.expiredAt")}
              </label>
            </Flex>
            <TextField.Root
              name="expiredAt"
              defaultValue={
                node.expired_at
                  ? new Date(node.expired_at).toISOString().slice(0, 10)
                  : "0001-01-01"
              }
              type="date"
            >
              <TextField.Slot side="right">
                <Button
                  type="button"
                  variant="ghost"
                  onClick={() => {
                    const dateInput = document.querySelector(
                      'input[name="expiredAt"]'
                    ) as HTMLInputElement;
                    if (dateInput) {
                      const futureDate = new Date();
                      futureDate.setFullYear(futureDate.getFullYear() + 200);
                      dateInput.value = futureDate.toISOString().slice(0, 10);
                    }
                  }}
                >
                  {t("admin.nodeTable.setToLongTerm", "设置为长期")}
                </Button>
              </TextField.Slot>
            </TextField.Root>
            <Flex gap="2" align="center"></Flex>
            <SettingCardSwitch
              title={t("admin.nodeTable.autoRenewal")}
              description={t("admin.nodeTable.autoRenewalDescription")}
              defaultChecked={node.auto_renewal || false}
              onChange={setAutoRenewal}
            />
            <Button type="submit" disabled={saving}>
              {t("common.save")}
            </Button>
          </Flex>
        </form>
      </Dialog.Content>
    </Dialog.Root>
  );
}
