# 仓库内其它文档

本站讲的是"怎么用"。项目的**工程记录**留在仓库里，没有发布到本站 —— 那些材料的读者是维护者，写给公众读者会两头不讨好。

下面是仓库 `docs/` 目录里仍然在维护、对运维者可能有用的部分（链接指向 GitHub，以仓库中的版本为准）：

| 文档 | 内容 |
|---|---|
| [`docs/AGENT-FOOTPRINT.md`](https://github.com/Aone2233/Nekomari/blob/main/docs/AGENT-FOOTPRINT.md) | 探针在主机上留下的痕迹：文件、服务、端口、系统调用 |
| [`docs/PERFORMANCE.md`](https://github.com/Aone2233/Nekomari/blob/main/docs/PERFORMANCE.md) | 性能特征与调优参数 |
| [`docs/AUTH-HARDENING.md`](https://github.com/Aone2233/Nekomari/blob/main/docs/AUTH-HARDENING.md) | 认证相关的加固措施 |
| [`docs/SECRETS.md`](https://github.com/Aone2233/Nekomari/blob/main/docs/SECRETS.md) | 密钥与 token 的处理约定 |
| [`docs/RESOURCE-HARDENING.md`](https://github.com/Aone2233/Nekomari/blob/main/docs/RESOURCE-HARDENING.md) | 资源限制与隔离 |
| [`docs/DEPLOY-VERIFICATION.md`](https://github.com/Aone2233/Nekomari/blob/main/docs/DEPLOY-VERIFICATION.md) | 部署后如何验证 |
| [`docs/RELEASING.md`](https://github.com/Aone2233/Nekomari/blob/main/docs/RELEASING.md) | 发布流程 |
| [`docs/THIRD-PARTY-LICENSES.md`](https://github.com/Aone2233/Nekomari/blob/main/docs/THIRD-PARTY-LICENSES.md) | 第三方组件与许可 |
| [`deploy/`](https://github.com/Aone2233/Nekomari/tree/main/deploy) | 安装脚本、nginx 配置、整队升级脚本 |

## 为什么不全部发布

`docs/` 里还有审查记录、事故复盘、路线图、发布跟进等约二十份文档。它们**不发布**的原因有三：

1. **读者不同。** 运维者需要的是"我该怎么做"，而不是"我们当时为什么这么做"。混在一起，前者要先在一堆后者里找自己那份。
2. **写作者会变。** 为内部记录的文档一旦面向公众，措辞会开始规避而不是说明问题 —— 而内部记录的价值恰恰在于直说。
3. **会过期。** 审查记录是对某一时刻的判断，保留在仓库里带日期是合适的；作为"文档"发布出去，读者会当成当前状态。

需要这些材料的读者，仓库本来就是公开的。
