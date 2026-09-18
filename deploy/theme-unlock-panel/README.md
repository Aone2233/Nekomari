# theme-unlock-panel/

给 LuminaPlus 的 IP 信息面板补上「流媒体 / AI 解锁」区块。

## 为什么需要这一层

主题**自己没有渲染解锁的代码**。这不是猜的，是查过的：

| 检查 | 结果 |
|---|---|
| LuminaPlus 1.3.3（在用的）全部 chunk 搜 `解锁` / `流媒体` / `Netflix` / `ChatGPT` | 0 命中 |
| 上游 1.3.4（下载解包后同样搜） | 0 命中 |
| 主题 CSS 里的 `ip-info-*` 类 | 只有 address / family-switch / detail-grid / section / latency / native-badge / refresh |
| 主题 IP 面板的 JSX | 只渲染三个 section：地理信息、网络信息、全球延迟检测 |
| `capabilities.media_unlock` / `ai_unlock` | **只出现在 zod schema 里**，没有任何代码读它 |
| 参考插件 `Komari-IP-Info` 的 `/status` | 自己写着 `media_unlock: false, ai_unlock: false` |

也就是说这两个布尔是主题作者留的占位，参考实现也没做。数据有了也没地方显示。

## 为什么是附加脚本，不是改主题

压缩过的 React bundle 改起来极易出错，而且主题一升级就失效。`unlock-panel.js` 只依赖
主题已经公开的两样东西：

* DOM：`.ip-info-panel`、`.ip-info-address strong`、`.ip-info-detail-grid`
* 接口：`GET /api/public/ip-info/v1/lookup`（主题自己就在用）

主题升级后如果这几处变了，区块会**不显示**而不是显示错的东西 —— 这是刻意的取舍。

## 安装

```bash
sudo ./install-theme-unlock-panel.sh
```

幂等：重复执行只覆盖脚本，不重复插标签。第一次会备份 `index.html.bak-pre-unlock`。

## 数据从哪来

节点上的探针测量后上报（`agent/unlock`），服务端存 `unlock_reports` 表，随
`/api/public/ip-info/v1/lookup` 的 `data.unlock` 返回。

必须由探针测，不能在服务端测：解锁取决于**发起请求的那个 IP**，服务端在别的机房，
只能测到它自己。

## 「未知」是什么意思

不是失败，是**诚实地测不出来**。第一轮实地探测就说明了原因：

* `chatgpt.com` 与 `claude.ai` 对数据中心出口**一律返回 403**（Cloudflare 风控），
  与地区封锁无法区分 —— 所以按状态码判断会得出错误结论
* `disneyplus.com` 与 `primevideo.com` 的页面里**本来就含** "unavailable" / "not available"，
  按子串判断毫无意义
* `gemini.google.com/cdn-cgi/trace` 返回 404（Google 不在 Cloudflare 上）

所以每个结论都带 `basis`：`probe` 表示真的读到了服务本身的信号，`region` 表示只判断了
出口地区。面板上会写明「按地区判断，未验证该 IP 是否真的可用」。

## 出口地址为什么单独存

因为**它不一定是节点自己的地址**。实测本机群里有一台中国探针，它的 HTTPS 出口是另一台
美国节点的地址。不把出口地址一起存下来，面板就会把结论算到错误的节点上。

## 标记是怎么定的

对着**未封锁和已封锁两份真实样本**定的，不是凭印象：

| 服务 | 信号 | 依据 |
|---|---|---|
| Netflix | 标题页里出现该标题 id（`81280792` 自制剧 / `80018499` 片库），**带重试** | 该请求会直接失败（连接被重置，只有 51 字节），一次失败不能当「未解锁」 |
| YouTube Premium | 明确写出的 `Premium is not available` | 未封锁样本出现 17 次 `ad-free` 且无该句；被封锁样本 `ad-free` 只剩 4 次并出现该句一次。**只数 `ad-free` 会判错** |
| ChatGPT / Claude | Cloudflare `trace` 的 `loc=` | 唯一一个不设防、任何出口都能拿到地区信息的端点 |

复现这两份样本用 `deploy/unlock-probe-recon.py` 与 `unlock-probe-recon2.py`。
