import React, { useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Button,
  Callout,
  Checkbox,
  Flex,
  SegmentedControl,
  Text,
  TextArea,
  TextField,
} from "@radix-ui/themes";
import { Copy, Radar, Settings } from "lucide-react";
import { Link } from "react-router-dom";
import { toast } from "sonner";
import Tips from "@/components/ui/tips";
import Loading from "@/components/loading";
import {
  quotePowerShellArg,
  quoteShellArg,
  quoteShellArgs,
} from "@/utils/shellQuote";
import type { Platform } from "../nodeTable/GenerateCommandButton";
import { useIsSnapshotBackend } from "../nodeTable/useIsSnapshotBackend";

/**
 * The "add node" dialog's body: the auto-discovery key, its settings, and the
 * one-liner a new node runs to enrol itself.
 *
 * It was ~860 of `pages/admin/index.tsx`'s lines. Moved verbatim; roadmap A1's
 * mounted fixture (`script/admin-node-table.browser.spec.py`) mounts this through
 * the page, so a mistake here fails there rather than in production.
 */
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


export const AutoDiscoverySection = ({
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
    })();    const args: string[] = ["-e", host, "--auto-discovery", adKey];
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
      <Flex
        data-testid="ad-section"
        align="center"
        justify="center"
        mt="4"
        py="4"
      >
        <Loading text="" />
      </Flex>
    );
  }

  if (!enabled) {
    return (
      <Callout.Root color="blue" mt="4" size="1" data-testid="ad-section">
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
    <Flex data-testid="ad-section" direction="column" gap="3" mt="4">
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
        data-testid="ad-platform"
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
