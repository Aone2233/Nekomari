# Nekomari

[English](./README.md) | [简体中文](./README_zh-cn.md)

> ### 🐱 Nekomari is a fork of [Komari](https://github.com/komari-monitor/komari)
>
> **Base version: `1.5.0-fix1` (commit `0ca87aa`, 2026-09-14)** — the last release published before upstream was archived.
> Upstream is **archived** and no longer maintained; Nekomari continues it with new features.
> See **[FORK.md](./FORK.md)** for full provenance and the change list.
>
> Licensed MIT, same as upstream. The original copyright (c) 2025 Komari Moniter is preserved verbatim in `LICENSE` and `NOTICE`.

Nekomari is a lightweight, self-hosted server monitoring solution — a maintained continuation of Komari. It provides a simple and efficient way to track server performance through a web interface, with metrics collected by a lightweight agent.

> [!WARNING]
> Nekomari is a self-hosted monitoring and control application. Deploy it only on systems you own or are authorized to manage. You are solely responsible for how you deploy and use it. The developers accept no liability for unauthorized access, persistence, command execution, other misuse, or any resulting consequences.

## What Nekomari adds over upstream

| Change | Status |
|---|---|
| **`netcheck`** — multi-protocol reachability probe (DNS + ICMP + TCP) that tells you which probe type a target actually supports | ✅ available |
| Monorepo layout — server + `frontend/` + `agent/` in one repository | ✅ |
| Bundled theme packer (`tools/zstdpack`) — builds without the `zstd` CLI (useful on Windows) | ✅ |
| One-shot build script (`build.sh`) | ✅ |
| Further features in progress | 🚧 see [FORK.md](./FORK.md) |

### `netcheck` in 20 seconds

Monitoring tasks probe a target with **one** protocol (ICMP, TCP or HTTP). Pick the wrong one and you get a flat 100% packet loss — which looks like an outage but is really a **protocol mismatch**. `netcheck` tells you up front which one to use:

```
$ ./nekomari netcheck 1.51.3.134

ICMP probe (3x, timeout 3s)
  x recv 0/3  loss 100%   -- target does not answer ICMP
TCP probe (timeout 3s)
  x 22     unreachable
  v 80     open   (handshake 2 ms)
  v 443    open   (handshake 2 ms)
----------------------------------------------------------
Verdict: TCP-only reachable (does not answer ICMP)
Advice:  this target does NOT answer ICMP, but TCP:80,443 is open. Use a `tcp`
         probe task and write the target as host:port (e.g. 1.51.3.134:80),
         otherwise you will get 100% packet loss.
```

It also distinguishes **"permission denied"** from **"target unreachable"** — otherwise a non-privileged ICMP attempt leads to the *opposite* conclusion.

## Features


- **Real-time monitoring**: Displays monitoring data at one-second intervals.
- **Lightweight and efficient**: Uses minimal system resources and works well on servers of any size.
- **Self-hosted**: Keeps you in control of your data and privacy.
- **Web interface**: Provides an intuitive, easy-to-use monitoring dashboard.
- **Extensible**: Supports custom themes and plugins.

## Quick Start

| Platform                                                                                                                                                                                                  | Description                                                                                                                                                                                           |
| --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| <a href="https://app.rainyun.com/apps/rca/store/6780/NzYxNzAz_"><img src="https://rainyun-apps.cn-nb1.rains3.com/materials/deploy-on-rainyun-cn.svg" alt="Rainyun" width="180"></a>                       | Deploy websites, databases, and hundreds of popular apps in seconds with flexible hourly billing. [Get started for just ¥5/month. Deploy now!](https://app.rainyun.com/apps/rca/store/6780/NzYxNzAz_) |
| <a href="https://apps.fit2cloud.com/1panel/komari"><img src="https://raw.githubusercontent.com/komari-monitor/public/refs/heads/main/images/1panel-logo-blue.png" alt="1Panel App Store" width="180"></a> | A modern, open-source Linux server management panel for websites, databases, containers, files, backups, security, and AI, with one-click deployment from its app store.                              |

For instructions on Docker deployment, binary installation, building from source, and updates, see the [installation guide](https://www.komari.wiki/en/install/quick-start).

## Screenshots

| Page                | Screenshot                                                                                                                                                             |
| ------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Home Dashboard      | <img src="https://b2.akz.moe/awesome-pictures/komari-screenshot/%E4%B8%BB%E9%A1%B5%E4%BB%AA%E8%A1%A8%E7%9B%98-en.webp" width="800" alt="Home Dashboard">               |
| Admin Dashboard     | <img src="https://b2.akz.moe/awesome-pictures/komari-screenshot/%E5%90%8E%E5%8F%B0%E4%BB%AA%E8%A1%A8%E7%9B%98-en.webp" width="800" alt="Admin Dashboard">              |
| History Charts      | <img src="https://b2.akz.moe/awesome-pictures/komari-screenshot/%E5%8E%86%E5%8F%B2%E5%9B%BE%E8%A1%A8-en.webp" width="800" alt="History Charts">                        |
| Web Terminal        | <img src="https://b2.akz.moe/awesome-pictures/komari-screenshot/%E7%BD%91%E9%A1%B5%E7%BB%88%E7%AB%AF.webp" width="800" alt="Web Terminal">                             |
| Customizable Themes | <img src="https://b2.akz.moe/awesome-pictures/komari-screenshot/%E4%B8%BB%E9%A2%98%E5%8F%AF%E8%87%AA%E5%AE%9A%E4%B9%89-en.webp" width="800" alt="Customizable Themes"> |
| Theme Market        | <img src="https://b2.akz.moe/awesome-pictures/komari-screenshot/%E4%B8%BB%E9%A2%98%E5%B8%82%E5%9C%BA-en.webp" width="800" alt="Theme Market">                          |

## Sponsors

Interested in sponsoring Komari? Contact the developer via [email](mailto:komari@akz.moe) or [Telegram](https://t.me/mamomoe).

| Sponsor                                                                                                                                                                                          | Description                                                                                                                                                                                                                                                                   |
| ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| <a href="https://axisnow.io/zh?utm=komari"><img src="https://raw.githubusercontent.com/komari-monitor/public/refs/heads/main/images/AxisNow.jpg" alt="AxisNow" width="180"></a> | [Self-Hosted Private CDN \| Subscription-Based CDN-Like Service \| A Fully Controlled, Flexible, Modular CDN Network](https://axisnow.io/zh?utm=komari) |
| <a href="https://whmcs.as211392.com/aff.php?aff=110"><img src="https://raw.githubusercontent.com/komari-monitor/public/refs/heads/main/images/dreamcloud.png" alt="Dream Cloud" width="180"></a> | Cost-effective Asia-Pacific hosting with direct connectivity and robust DDoS protection, backed by transparent capacity claims.                                                                                                                                               |
| <a href="https://sharon.io"><img src="https://raw.githubusercontent.com/komari-monitor/public/refs/heads/main/images/sharon-networks.webp" alt="Sharon Networks" width="180"></a>                | Premium China-optimized connectivity from Asia-Pacific data centers, featuring low latency, high bandwidth, and Tbps-scale local DDoS mitigation. Join the [Telegram community](https://t.me/SharonNetwork) to participate in charitable initiatives and community giveaways. |

## Contributors

Thanks to everyone who has contributed code, themes, plugins, documentation, translations, bug reports, or feedback to Komari.

<a href="https://github.com/komari-monitor/komari/graphs/contributors"><img src="https://contributors-img.web.app/image?repo=komari-monitor/komari" alt="Komari contributors" width="600"></a>

## Support the Project

If Komari has been useful to you, consider buying me a coffee. Thank you for your support!

| WeChat Pay                                                                                                   | TRON Network                                                                                |
| ------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------- |
| ![WeChat Pay QR code](https://b2.akz.moe/awesome-pictures/%E5%BE%AE%E4%BF%A1%E8%B5%9E%E8%B5%8F%E7%A0%81.png) | ![TRON Network QR code](https://b2.akz.moe/awesome-pictures/PixPin_2026-08-07_15-16-52.png) |
