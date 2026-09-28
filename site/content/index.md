<div class="nk-hero" markdown>

# Nekomari

**上游 Komari 的延续分支。** 服务器监控面板 + 探针，重点在于**可运维**：整队升级可以按节点指定版本、也能回滚，出问题时面板会告诉你原因而不是让你猜。

[开始安装](get-started/install.md){ .md-button .md-button--primary }
[与上游的关系](introduction/lineage.md){ .md-button }
[GitHub 仓库](https://github.com/Aone2233/Nekomari){ .md-button }

</div>

<div class="nk-grid" markdown>

<div class="nk-card" markdown>
### 监控
CPU、内存、磁盘、负载、网络吞吐与时延，多线路 Ping 与丢包统计，按分组与区域聚合。
</div>

<div class="nk-card" markdown>
### 告警
离线、负载、流量、到期、SLA 违约与流量预测，支持 Telegram、Bark、邮件、Gotify、Server 酱³ 等通道。
</div>

<div class="nk-card" markdown>
### 运维
批量执行、终端、维护窗口、批量配置导入导出、面板触发的探针升级（含按节点指定版本与回滚）。
</div>

<div class="nk-card" markdown>
### 可观测
Prometheus 风格指标落盘、SLA 报表、流量与成本预测、pprof 性能分析。
</div>

</div>

## 这个分支和上游有什么不同

上游已于 2026-09 归档，本分支以 `1.5.0-fix1` 为基线继续维护。相对上游的主要变化：

- **面板自带的独立后台**，不再依赖主题提供后台界面 —— 安装任何主题都不会让后台消失
- **探针升级由面板下发**：可指定版本、可回滚，升级失败会留下日志而不是静默跳过
- **新增运维能力**：批量执行、维护窗口、配置批量导入导出、SLA 报表、流量与成本预测
- **版本号接续上游编号**（`1.6.0` 起），使继承来的插件市场能正确识别本分支

完整清单与血缘记录见 [与上游 Komari 的关系](introduction/lineage.md)。

## 需要更深入的材料？

本站在讲解"怎么用"。项目的工程记录（性能分析、结构性审查、发布流程、事故复盘等）保留在仓库的 `docs/` 目录，未发布到本站 —— 见 [仓库内其它文档](reference/repository-docs.md)。
