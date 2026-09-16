# Nekomari

[English](./README.md) | [简体中文](./README_zh-cn.md)

> ### 🐱 Nekomari 是 [Komari](https://github.com/komari-monitor/komari) 的 fork
>
> **基线版本：`1.5.0-fix1`（commit `0ca87aa`，2026-09-14）** —— 上游归档前发布的最后一个版本。
> 上游仓库已 **归档**、不再维护；Nekomari 在其基础上继续开发新功能。
> 完整的来源说明与改动清单见 **[FORK.md](./FORK.md)**。
>
> 许可证与上游一致（MIT）。原始版权声明 `Copyright (c) 2025 Komari Moniter` 已在 `LICENSE` 与 `NOTICE` 中**原样保留**。

Nekomari 是一款轻量级的自托管服务器监控工具 —— Komari 的持续维护版本。它支持通过 Web 界面查看服务器状态，并通过轻量级 Agent 收集数据。

> [!WARNING]
> Nekomari 是一款自托管的监控/控制程序，仅应部署在你拥有或已获得授权管理的系统上。在未获授权的情况下部署、访问、持久化、执行命令及从事其他滥用行为，用户需要自行承担部署和使用的责任。开发者不对未经授权或滥用行为及其后果承担责任。

## Nekomari 相对上游新增了什么

| 改动 | 状态 |
|---|---|
| **`netcheck`** —— 多协议可达性自检（DNS + ICMP + TCP），直接告诉你目标该用哪种探测类型 | ✅ 已可用 |
| 单仓结构 —— 服务端 + `frontend/` + `agent/` 合并到一个仓库 | ✅ |
| 自带主题打包器（`tools/zstdpack`）—— 无需 `zstd` CLI 即可构建（Windows 上尤其实用） | ✅ |
| 一键构建脚本（`build.sh`） | ✅ |
| 更多功能开发中 | 🚧 见 [FORK.md](./FORK.md) |

### 20 秒看懂 `netcheck`

监控任务对目标只用**一种**协议探测（ICMP / TCP / HTTP）。**选错协议就会得到恒定的 100% 丢包** —— 看起来像宕机，实际是协议不匹配。`netcheck` 让你提前知道该选哪种：

```
$ ./nekomari netcheck 1.51.3.134

ICMP 探测 (3 次, 单次超时 3s)
  ✗ 收 0/3  丢包 100%   —— 目标不响应 ICMP
TCP 探测 (单次超时 3s)
  ✗ 22     不可达
  ✓ 80     开放   (握手 2 ms)
  ✓ 443    开放   (握手 2 ms)
----------------------------------------------------------
判定: 仅 TCP 可达（不响应 ICMP）
建议: 该目标【不响应 ICMP】，但 TCP:80,443 可用。监测任务必须选 tcp，
      目标写成 host:port（例如 1.51.3.134:80），否则会 100% 超时。
```

它还会刻意区分 **「权限不足」与「目标不可达」** —— 否则非特权环境下的 ICMP 失败会得出**完全相反**的结论。


## 特性

- **实时监控**: 秒级实时数据展示。
- **轻量高效**：低资源占用，适合各种规模的服务器。
- **自托管**：完全掌控数据隐私，部署简单。
- **Web 界面**：直观的监控仪表盘，易于使用。
- **极强的可扩展性**: 支持自定义主题和插件。

## 快速开始

| 平台                                                                                                                                                                                                     | 介绍                                                                                                                                   |
| -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------- |
| <a href="https://app.rainyun.com/apps/rca/store/6780/NzYxNzAz_"><img src="https://rainyun-apps.cn-nb1.rains3.com/materials/deploy-on-rainyun-cn.svg" alt="Rainyun" width="180"></a>                      | 秒级部署网站、数据库及数百款热门 App，并采用按小时灵活计费。[每月5元，立即部署](https://app.rainyun.com/apps/rca/store/6780/NzYxNzAz_) |
| <a href="https://apps.fit2cloud.com/1panel/komari"><img src="https://raw.githubusercontent.com/komari-monitor/public/refs/heads/main/images/1panel-logo-blue.png" alt="1Panel Appstore" width="180"></a> | 现代化、开源的Linux 服务器运维管理面板，提供网站、数据库、容器、文件、备份、安全与AI 管理能力，支持应用商店一键部署。                  |

Docker、二进制文件、源码构建和更新说明，请参阅 [安装指南](https://www.komari.wiki/install/quick-start).

## 截图

| 页面         | 截图                                                                                                                                                         |
| ------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 主页仪表盘   | <img src="https://b2.akz.moe/awesome-pictures/komari-screenshot/%E4%B8%BB%E9%A1%B5%E4%BB%AA%E8%A1%A8%E7%9B%98.webp" width="800" alt="主页仪表盘">            |
| 后台仪表盘   | <img src="https://b2.akz.moe/awesome-pictures/komari-screenshot/%E5%90%8E%E5%8F%B0%E4%BB%AA%E8%A1%A8%E7%9B%98.webp" width="800" alt="后台仪表盘">            |
| 历史图表     | <img src="https://b2.akz.moe/awesome-pictures/komari-screenshot/%E5%8E%86%E5%8F%B2%E5%9B%BE%E8%A1%A8.webp" width="800" alt="历史图表">                       |
| 网页终端     | <img src="https://b2.akz.moe/awesome-pictures/komari-screenshot/%E7%BD%91%E9%A1%B5%E7%BB%88%E7%AB%AF.webp" width="800" alt="网页终端">                       |
| 主题可自定义 | <img src="https://b2.akz.moe/awesome-pictures/komari-screenshot/%E4%B8%BB%E9%A2%98%E5%8F%AF%E8%87%AA%E5%AE%9A%E4%B9%89.webp" width="800" alt="主题可自定义"> |
| 主题市场     | <img src="https://b2.akz.moe/awesome-pictures/komari-screenshot/%E4%B8%BB%E9%A2%98%E5%B8%82%E5%9C%BA.webp" width="800" alt="主题市场">                       |

## 赞助商

有意赞助 Komari？请通过 [电子邮箱](mailto:komari@akz.moe) 或 [Telegram](https://t.me/mamomoe) 联系开发者。

| 赞助商                                                                                                                                                                                           | 描述                                                                                                                                                                                                                |
| ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| <a href="https://axisnow.io/zh?utm=komari"><img src="https://raw.githubusercontent.com/komari-monitor/public/refs/heads/main/images/AxisNow.jpg" alt="AxisNow" width="180"></a> | [自建私有部署CDN \| 订阅式高仿CDN \| 自主可控、灵活组合的CDN网络](https://axisnow.io/zh?utm=komari) |
| <a href="https://whmcs.as211392.com/aff.php?aff=110"><img src="https://raw.githubusercontent.com/komari-monitor/public/refs/heads/main/images/dreamcloud.png" alt="Dream Cloud" width="180"></a> | 极高性价比解锁直连亚太高防，真高防，不虚标，打死退款                                                                                                                                                                |
| <a href="https://sharon.io"><img src="https://raw.githubusercontent.com/komari-monitor/public/refs/heads/main/images/sharon-networks.webp" alt="Sharon Networks" width="180"></a>                | 亚太数据中心提供顶级的中国优化网络接入 · 低延时&高带宽&提供Tbps级本地清洗高防服务, 为您的业务保驾护航, 为您的客户提供极致体验. 加入社区 [Telegram群组](https://t.me/SharonNetwork) 可参与公益募捐或群内抽奖免费使用 |

## 贡献者

感谢所有为 Komari 贡献代码、主题、插件、文档、翻译、问题报告或反馈的朋友。

<a href="https://github.com/komari-monitor/komari/graphs/contributors"><img src="https://contributors-img.web.app/image?repo=komari-monitor/komari" alt="Komari 贡献者" width="600"></a>

## 支持项目

如果 Komari 对你有所帮助，欢迎请作者喝一杯奶茶。感谢你的支持！

| 微信赞赏码                                                                                       | TRON Network                                                                |
| ------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------- |
| ![wechat](https://b2.akz.moe/awesome-pictures/%E5%BE%AE%E4%BF%A1%E8%B5%9E%E8%B5%8F%E7%A0%81.png) | ![TRON](https://b2.akz.moe/awesome-pictures/PixPin_2026-08-07_15-16-52.png) |
