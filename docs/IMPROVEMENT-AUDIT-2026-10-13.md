# 改进审计 — 2026-10-13

一次系统性的"还需要改什么"检查。四条只读审计线（服务端 Go / 前端 TS / agent+部署+CI / 文档对照实际）
并行跑，每条结论都由本文作者对当前代码复核过；文中每条都标了 **VERIFIED**（跑过或读到代码）或
**INFERRED**（只读推理）。本文只新增本文件，未改动任何其它文件，未提交。

## 0. 基线、方法与口径

| 项 | 值 |
|---|---|
| 分支 / HEAD | `main` @ `4e7d389`，**恰好等于 tag `v1.6.10`**（2026-10-05），`git log v1.6.10..main` 为空 |
| 生产实际部署 | **`v1.6.9`**（`docs/DEPLOY-OC424.md:159-165`）——又一次"已发布 ≠ 已部署" |
| 工作区 | ` M README.md`（+1 行，给新文档加索引）、`?? docs/OC424-MTPROTO.md`、`?? topology/` |
| 本地实测 | `go build ./...` 0、`go vet ./...` 0、`go test ./... -count=1` **全绿**；`npx tsc --noEmit` 0、`npm run lint` 0、`npm test` **129/129** |
| 跟踪文件 | 1548 个；无 tracked `node_modules`；最大跟踪文件 5.14 MB |

**口径。** 本文不重复 `docs/audits/2026-10-01/FINDINGS-STATUS-2026-10-01.md` 的 55 条清单，只做两件事：
(a) 把那批里**至今仍未修**的条目重新对码确认；(b) 记录那一批**没有覆盖**的新发现。
凡文档说"已修"而代码不支持的，以代码为准并指出文档错在哪。

## 1. 结论摘要

代码本身是健康的：全量 Go 测试、vet、tsc、eslint、前端单测都过，没有 TODO/FIXME 残留，
依赖有 CVE 注释跟踪，凭据随机数用的是 `crypto/rand` 拒绝采样。**问题不在代码烂，而在四处**：

1. **一条匿名的信息泄露链**，把隐藏节点的 UUID 和"哪些 UUID 是隐藏节点"同时交给未登录访客；
2. **发布链路有五个能"静默出错"的口子**——没有 CI 门禁、版本号契约不强制、手动发布的 Release 拿不到镜像、
   校验和清单可能不完整、本地构建的 agent 会自我替换成官方版；
3. **一批"工具已经写好但没有任何流水线在跑"的门禁**——`audit:compiler`（当前**正在失败**）、
   i18n 键位一致性、文档站构建、四个 action 检查脚本；
4. **文档与实际状态脱节**：CHANGELOG 缺 5 个版本的条目，ROADMAP 的状态表停在 v0.1.30，
   而唯一完整的开项台账是一份已经过期数天的快照。

## 2. P0 — 安全

### 2.1 匿名可枚举隐藏节点，并区分"隐藏"与"不存在"（HIGH，VERIFIED）

三处叠成一条链，全部**匿名可达**（默认 `private_site=false`）：

| # | 位置 | 行为 |
|---|---|---|
| 1 | `web/rpc/jsonrpc/public.go:243-270`（`:263` 返回 `task.Clients`） | `public:getPublicPingTasks` 原样回显每个 ping 任务的 `clients` 数组，**不做任何可见性过滤** |
| 2 | `web/rpc/jsonrpc/common.go:508-513` | `common:getNodeRecentStatus`：hidden → `InvalidParams "UUID is required"`；不存在的 uuid → `{count:0,records:[]}` |
| 3 | `web/rpc/jsonrpc/common.record.go:77-78` | `common:getRecords`：hidden → `"UUID not found"`；不存在的 uuid → `"Failed to fetch records"` |

**可达性已逐层确认**：`public.go:31` `regPublic(...)` + `public.go:23` `rpc.Allow("public:*", rpc.RoleGuest)`；
`pkg/rpc/permission.go:52` `Allow("common:*", RoleGuest)`，且 `resolveMinRole` 把裸方法名归一化到
`common:`（`permission.go:170-176`）；`web/rpc/jsonrpc/dispatch.go:45-49` 的私有站点拦截只在
`private_site=true` 时生效，公开面板下匿名直接通过；HTTP 入口 `web/router/router.go:66`（`GET /api/task/ping`）
与 `:69-70`（`/api/rpc2`）。

**修法（仓库里已有现成且被测试覆盖的正确实现）**：`clientHistoryReadable`（`web/rpc/jsonrpc/public.go:57`，
已用于 `:160/:180/:366/:400` 与 `public.metric.go:1164/:1177`）与
`database/unlock/unlock.go:60` 的 `ClientHistoryReadable` / `:77` `LoadVisible`。要做的是：
让 hidden 与 unknown 的回答**逐字相同**，并过滤 `getPublicPingTasks` 回显的 uuid。
`web/rpc/jsonrpc/public_history_test.go:17-60` 已经是这个模式的测试样板，照抄即可。
**注意主题契约**——这几处都是公开 RPC 载荷，`docs/OPEN-WORK.md` 条目 2 的教训在这里适用。

**根因（这才是要修的东西）**：`Allow("common:*", RoleGuest)` 意味着**任何新增的裸命名空间 RPC 自动公开**。
`web/rpc/jsonrpc/dispatch_test.go:22-46` 已经为"私有站点白名单"写了这种枚举测试，
但**没有**为 guest ACL 写同样的测试。建议补一个：遍历所有已注册方法，算出 `RequiredRole`，
断言每个 guest 可达的方法都在一份**显式、经过审阅**的名单上——这样新增方法会强制一次决定。

### 2.2 任意 SQL 的管理 RPC 免 step-up 2FA，且读操作不审计（HIGH，VERIFIED）

`web/rpc/jsonrpc/admin.dbquery.go:66`（`dbQuery`）与 `:77`（`dbExec`）只要求 `rpc.RoleAdmin`。
全仓库 `MarkSensitive` **只有一处**：`web/rpc/jsonrpc/admin.system.go:46` `rpc.MarkSensitive("admin:exec")`；
而 step-up 只在 `web/rpc/jsonrpc/transport.go:52-53` 的 `rpc.IsSensitive(req.Method)` 为真时施加。

同一文件内部就不一致：`adminDBExec` 写审计（`admin.dbquery.go:124-125`），`adminDBQuery` 不写。

按本仓库自己的标准（`web/router/router.go:138/146-147/156` 给改凭据和远程执行都挂了 `RequireSensitive2FA`），
任意 SQL（改写 `users.passwd`、读 `configs.api_key`、DROP TABLE）至少同等敏感。
**修法**：两处都 `MarkSensitive`，`adminDBQuery` 补审计，`limit` 已有 `maxDBQueryLimit` 保护（`:181`）。

### 2.3 插件的持久化数据不在备份白名单里——一个拼写错误（HIGH，VERIFIED）

`web/api/admin/backup_whitelist.go:23` 写的是 `"plguin-data/"`；运行时目录是
`internal/plugin/plugin.go:51` 的 `var StorageDir = "./data/plugin-data"`——文档注释（`plugin.go:46-50`）
明确说它"跨插件重装存活"、以 `__storageDir__` 暴露给插件（写入见 `:273-281`）。

`plguin-data` 全仓库**只出现这一次**，`plugin-data` 出现 6 次。而
`copyWhitelistedFilesFrom`（`backup_whitelist.go:36-40`）对不存在的条目**静默跳过**。
后果：备份报告成功，而每个插件的持久状态都没进备份——等运维依赖它时已经无法挽回。
**修法**：改掉这个字面量；并加一条断言：运行时在 `./data` 下写的每个目录，要么在白名单里，要么被显式说明为何不备份。

### 2.4 管理表单用 `new Function` 执行任意 JS（MEDIUM，VERIFIED）

`frontend/src/utils/unitHelper.ts:74` `const value = new Function(\`return ${numericPart}\`)();`，
由节点编辑对话框的每次按键触发（`frontend/src/pages/admin/nodeTable/NodeDialogs.tsx:535`）。
数值部分只剥掉了单位后缀就拼进函数体。注释自称"假设输入源可信"——但它处理的是**粘贴进来的表单文本**。
**修法**：正则解析（`^([0-9.eE+-]+)\s*([a-z]*)$`）+ `Number()`，`128*1024gb` 这类用显式的两因子拆分保留。

### 2.5 市场下载的 SSRF 开关默认关闭，而默认市场源是第三方 URL（MEDIUM，VERIFIED）

`internal/config/settings.go:20` `SSRFProtectionEnabled ... default:"false"`（注释：默认关闭），
`web/api/admin/market_download.go:57-65` 只有开关为真时才用做地址校验的 transport；
而 `web/api/admin/theme_market.go:24` 把 `raw.githubusercontent.com/komari-monitor/theme-market/...`
作为**默认启用**的市场源，抓取目标由远端目录内容决定，不由运维决定。
（transport 本身是好的：`dialMarketAddress` 校验每个解析出的 IP，重定向在 `:69-74` 重新校验。）
另有 `web/api/admin/market_download.go:196-208` 的 `ssrfProtectionEnabled()` 在记录不存在时
**写入 `false` 并返回 `false`**——fail-open，且与同文件 `:41-43` 声称的 fail-closed 相反。
**修法**：默认改 `true`，或把默认市场源改为需显式启用。

### 2.6 密钥比较不是恒定时间（LOW，VERIFIED）

`web/api/Auth.go:162` `tempKey != allowKey`、`:243` `apiKey == "Bearer "+apiKeyConfig`、
`web/security/origin.go:58` 同样的 `==`、`web/api/client/autoDiscovery.go:24` `!=`。
AutoDiscovery 端点未认证、成功即建 client 行（`:34`）且自身无限流。
**修法**：三处改 `crypto/subtle.ConstantTimeCompare`；AutoDiscovery 接上已有的限流器。

## 3. P0 — 发布链路（每一条都能让一次发布静默出错）

### 3.1 `release.yml` 没有 CI 门禁（BLOCKER，VERIFIED）

`.github/workflows/release.yml:22` 触发条件是 `push: tags: ['v*']`；job 依赖只有
`build: needs: resolve`（`:96`）、`build-agent-darwin: needs: resolve`（`:264`）、
`release: needs: [resolve, build, build-agent-darwin]`（`:332`）。**没有 `workflow_run`、没有 `go test`、没有 `npm test`。**
规则只存在于散文里：`docs/RELEASING.md:14-23` 说"打 tag 前必须等 CI 在那个 commit 上变绿"，
并引用了 v0.1.10 就是"CI 红着打了 tag"的先例——也就是说，那个事故今天**仍然可以原样复现**。
**修法**：加一个 `gate` job，用 `gh run list --commit $SHA --workflow ci.yml --json conclusion`
（或 check-runs API）断言 `needs.resolve.outputs.sha` 上的 ci 是 success，让 `build` 依赖它。

### 3.2 版本号契约没有强制，而且两端的版本门都是 fail-open（BLOCKER，VERIFIED）

`FORK.md:39-48` 断言"发布工作流写进二进制的版本必须是纯 `X.Y.Z`"，理由是"两端都只接受数字点分版本"。
代码现在不是这样：

| 端 | 位置 | 不可解析时的行为 |
|---|---|---|
| 服务端 | `internal/plugin/version.go:33-37` | `parseSemver` 失败 → `return nil`（**放行**，注释写明"a malformed server version must not block plugin loading"） |
| 面板 | `frontend/src/pages/admin/market/plugins.tsx:114-116` | `if (!requirement || !currentVersion) return true`（**放行**） |

所以 `FORK.md` 描述的"面板判定为不满足、服务器放行（worst of both）"**已经不成立**：
今天是两端都放行，即**版本不可比较时插件门整体失效**——包括那些真的需要新 API 的插件。
这比文档描述的方向更糟，而 `FORK.md` 的整套编号论证正建立在那段描述上。

两处具体的引信：
- `build.sh:56` `VERSION="$(git describe --tags --always)"` → 在任何 tag 之后有提交时得到
  `v1.6.10-3-g4e7d389`，两端都不可解析。`build.sh:65` 构建 agent 时**完全没有注入版本**（`agent/update.CurrentVersion` 保持 `0.0.1`）。
- `.github/workflows/release.yml:60-64` 的 tag 校验正则 `^v[0-9A-Za-z][0-9A-Za-z._+-]*$`
  允许 `v1.6.11-rc1`；`:204-219` 唯一的断言是 `grep -aq -- "$want"`，只证明**字符串在**，不证明**可解析**。

**修法**：tag 正则收紧为 `^v[0-9]+\.[0-9]+\.[0-9]+$`（或直接解析）；
把两端的 fail-open 改成"版本不可解析时明确报出"；`build.sh` 用 `git describe --tags --abbrev=0`；
在 release.yml 里带注入版本跑 `go test ./internal/plugin/ -run TestTheShippedVersionParses`；同步修正 `FORK.md`。

### 3.3 手动 dispatch 发布的 Release 拿不到 Docker 镜像（HIGH，VERIFIED）

`release.yml` 用 `GITHUB_TOKEN` 建 Release，而 GitHub 会抑制由 `GITHUB_TOKEN` 触发的事件——
所以 `docker.yml:27-28` 的 `release: published` 不会触发，实际靠 `workflow_run`（`:33-35`）。
`docker.yml:113-116` 在 `workflow_run` 分支把 tag 取自 `github.event.workflow_run.head_branch`；
对一次 **`workflow_dispatch` 触发的 release**，`head_branch` 是**分支名**（`main`），
于是 `:117-120` 的 `case "$TAG" in v*)` 不匹配 → 报错退出，**镜像不构建**。
而 `docs/RELEASING.md:96-99` 说的正好相反，`deploy/docker-workflow-check.py:197-198` 还把同一个错误假设写进了真值表，
所以检查通过而现实不符。
**修法**：`workflow_run` 分支只在 `head_branch` 以 `v` 开头时才当 tag，否则由 `head_sha` 反查 tag；
把这一情形加进真值表。

### 3.4 校验和清单可能不完整（HIGH，VERIFIED）

`.github/workflows/release.yml:355-357`：

```yaml
- name: Generate checksums
  working-directory: dist
  run: sha256sum * | tee SHA256SUMS.txt
```

没有 `shell: bash`、没有 `set -o pipefail`（Linux runner 默认是 `bash -e {0}`），所以管道取 `tee` 的退出码，
`sha256sum` 失败被吞。下游对缺失条目是**硬失败**：`agent/update/update.go:409-420` 直接拒绝更新
（"release checksum missing for %s"），`deploy/install-node-agent.sh:250-253` 也会死。
即一份不完整清单会让**全队自动更新静默停止**。
**修法**：`set -euo pipefail` + `sha256sum * > SHA256SUMS.txt`，并在发布前断言行数与预期资产数一致。

### 3.5 `agent/build_all.*` 注入的是前 fork 的模块路径（HIGH，VERIFIED）

`agent/build_all.sh:41` 与 `agent/build_all.ps1:49`：

```
-ldflags="-X github.com/komari-monitor/komari-agent/update.CurrentVersion=${VERSION}"
```

而模块路径是 `github.com/Aone2233/nekomari/agent`（`agent/go.mod:1`）。`RELEASING.md:256` 自己写了
"`-X` 里的包路径写错会静默失败"。于是用这两个脚本构建的 agent 版本号停在 `0.0.1`，
启动时 `agent/cmd/root.go:260-266` 调 `update.CheckAndUpdate()`，
`agent/update/update.go:278-306` 把 0.0.1 当稳定版、挑最新 release、`os.Exit(42)`——
**刚构建出来的二进制会把自己替换成官方资产**。
**修法**：改对路径；`build.sh:65` 也补上版本注入（或在开发文档里明确要求 `--disable-auto-update`）。

### 3.6 发布验证只跑 linux/amd64，而参考面板主机是 arm64（HIGH，VERIFIED）

`deploy/deploy-verify.sh:71` 硬编码 `nekomari-linux-amd64` / `komari-agent-linux-amd64`；
`docs/DEPLOY-OC424.md:161` 记着 `arch=arm64 on an aarch64 host`。
arm64 与 darwin 的交叉编译路径（`release.yml:107-111`、`:262-269`）不在 `ci.yml` 的矩阵里
（`ci.yml:170` 只有 ubuntu/windows），**只在发布时才第一次被执行**。
**修法**：`deploy-verify.sh` 参数化架构，加一条 arm64 验证腿；CI 里加一个 aarch64 交叉编译 job。

## 4. P1 — 数字正确性

### 4.1 配额告警与流量预测用的是"开机累计"，不是"本周期用量"（HIGH，VERIFIED）

`utils/notifier/traffic.go:66`：

```go
used := computeUsedByType(strings.ToLower(c.TrafficLimitType), r.Network.TotalUp, r.Network.TotalDown)
```

`protocol/v2/jsonrpc.go` 的注释把语义写得很清楚：`TotalUp/TotalDown` 是**内核自开机以来的累计字节数**，
`CycleUp/CycleDown` 才是**自计费周期重置日以来**的累计。
而 `c.TrafficLimit` 是**周期配额**——所以这个百分比是"开机累计 ÷ 月配额"。

预测那一路同样：`utils/notifier/traffic_forecast.go:227-228` 读 `net.total.up` / `net.total.down`
（即 TotalUp/Down 的存储名，`internal/metricstore/report_mapping.go:36-37`），
并在 `:211-217` 的注释里断言"这就是限额被检查的那个量"——**该断言与事实相反**。
正确数据源是 `traffic.up` / `traffic.down`（`report_mapping.go:38-39`），
也就是首页流量卡片这次刚改成的那一路（HEAD `4e7d389`，P1-3 已修）。

**为什么测试看不见**：`utils/notifier/traffic_forecast_test.go` 用合成的 `forecast.Request`，
看不到样本取自哪个序列，换错序列测试仍然全绿。

### 4.2 SLA 把同一 task 的不同 tag 世代直接拼接（MEDIUM，VERIFIED）

`internal/sla/query.go:305` `tasks[taskIDOf(bucket.Tags)] = append(tasks[taskIDOf(bucket.Tags)], bucket.Samples...)`。
ROADMAP H1a 已经为指标路径在 `ReadSeriesPerEntity` 加了 `dedupe()`，但**这条路径没有**。
世代窗口重叠时同一个桶会出现多次（H1a 在线上实测 327/489 个相邻间隔为 0），
于是分位数、可用率、覆盖率的输入里同一个时刻被计了多次。

### 4.3 预测的两个边界（MEDIUM，INFERRED）

- `internal/forecast/forecast.go:250` 只在 `bytesPerSecond < 0`（净下降）时识别为"计数器重置"，
  一次仍然净增的重置会被当成真实用量；
- `:289-290` `time.Duration(secondsToLimit * float64(time.Second))` 无上界，大值溢出。

## 5. P1 — 门禁缺失（工具已经写好，但没有任何流水线在跑）

### 5.1 `audit:compiler` 正在失败，且不在 CI（MEDIUM，VERIFIED）

实跑结果：

```
$ cd frontend && npm run audit:compiler
C:\...\frontend\src\pages\sla\index.tsx:67:10
  67 |     void load(selectedWindow);
     |          ^^^^ Avoid calling setState() directly within an effect
✖ 1 problem (1 error, 0 warnings)          exit=1
```

而 `.github/workflows/ci.yml:196-200` 只跑 `npm ci` / `npm test` / `npm run lint` / `npm run build`，
`audit:compiler` 在**任何** workflow 里都没有出现。
`eslint.config.js:23-31` 只启用了 8 条 react-hooks 规则，所以 `--max-warnings 0` 对同一个文件返回 0——
**lint 通过不等于编译器门通过**。

文档两边都错：`docs/ROADMAP.md:599` 说该审计"in CI and currently reports 0"（两半都不成立），
A4 说 133 条（实际 1 条）。反倒是 `docs/TESTING.md:73-74` 的描述是对的。

### 5.2 i18n：三种语言各缺 134 个键，且没有一致性测试（HIGH，VERIFIED）

把 locale JSON 展平后实测：

| locale | 键数 | 相对 en 缺失 |
|---|---|---|
| `en` | 1461 | — |
| `zh_CN` | 1461 | 0 |
| `id_ID` | 1327 | **134** |
| `ja_JP` | 1327 | **134** |
| `zh_TW` | 1327 | **134** |

缺口集中在 v0.1.31–v0.1.38 那六个新功能：`bulk.*` 32、`status.*` 25、`forecast.*` 24、
`maintenance.*` 24、`config.*` 23、`admin.*` 6（顶层命名空间口径）。
`frontend/src/i18n/config.ts:83` 是 `fallbackLng: "en-US"`，没有 `missingKeyHandler`，
所以运行时和 CI 都看不见这个缺口——日/繁/印尼用户在这六个功能上直接看到英文。

两个附带的坑：
- `frontend/script/i18n-sync.mjs:17` 需要 `OPENAI_API_KEY`；`:123` 无 key 时对每一对返回空串，
  但脚本**仍然会重写文件**，且 `diffAndMerge`（`:187-203`）的 `result` 只包含源 locale 的键——
  也就是说**没有 key 时运行它会删掉目标语言里源语言没有的键**。
- `frontend/script/add-{bulk,config,forecast,maintenance,status}-locale-keys.py` 五个一次性脚本
  在全仓库**无任何引用**，它们正是这个缺口的化石。

**修法**：加 `frontend/script/i18n-parity.test.mjs`（`npm test` 的 `script/*.test.mjs` glob 会自动捡到），
断言每个 locale 的键集合与 en 相同、占位符一致；把 sync 改成并集语义（永不删除）。

### 5.3 文档站没有 CI，且引用了不存在的脚本（LOW-MEDIUM，VERIFIED）

`site/README.md` 说 `check` 步骤"not optional"（它把"构建成功"变成"导航里每个页面都存在"），
`deploy/docs/Dockerfile:19` 确实跑了 `node site/script/build.mjs stage && ... check`，
但 `.github/workflows/` 里**没有任何 job** 跑它——站点是发布在 `https://nekomari-docs.66040321.xyz` 的。
同一份 `site/README.md` 指向 `deploy/docs/build.sh`，**该文件不存在**（实际是 `deploy/docs/deploy.sh` + `Dockerfile`）。

### 5.4 四个 action 检查脚本不在任何 workflow 里（LOW-MEDIUM，VERIFIED）

`deploy/check-action-versions.py`、`check-action-breaking.py`、`check-action-inputs.py`、`bump-actions.py`
只被 `deploy/README.md:144-147` 提到。`ci.yml` 的 `workflow-contracts` job（`:150-162`）只跑
`docker-workflow-check.py`。没有 `.github/dependabot.yml`。
而所有 `uses:` 都是浮动大版本（`actions/checkout@v7`、`softprops/action-gh-release@v3` …），
没有 SHA 钉住。`check-action-inputs.py` 最要紧：一个被上游删掉的 `with:` 输入**只在 workflow 运行时才失败**——
对 `release.yml` 就是在发布时。

## 6. P1 — 数据安全（潜伏，出事才暴露）

| 项 | 位置 | 问题 |
|---|---|---|
| 整层 rollup 被无条件删除 | `pkg/metric/store.go:1289-1292`、`:1298-1303`、`:1312-1318`、`:1333` | `RetentionDays == 0` 的指标进 `disabled` → `deleteRollupsForMetricsTx` 删**该指标全部** rollup；指标 policy 里没有的 tier → `all: true` → 无条件删。内建指标默认保留 1 天（`internal/metricstore/definitions.go:11`），所以触发条件需要非默认保留期配置——**本文无法从仓库确认线上是否已具备条件（INFERRED，潜伏）** |
| 恢复页改写迁移目标键 | `internal/metricstore/store.go:159`、`:175` | 写入 `MigrationTargetKey = target`，迁移逻辑以它为源，源==目标即拒绝（`store_migration.go:82/:101-103`） |
| prune 脚本裸跑校验 | `deploy/prune-upgrade-backups.sh:42-50` | `set -euo pipefail`（`:13`）下 `python3 - "$f"` 未包在 `if !` 里；一个坏档（或 zipfile 抛异常）会**中止整个 prune**，而它在"保留"循环里，所以连过期备份都不会被删。5 行修复 |
| 会话缓存持全局锁跨 DB 查询，且不缓存 miss | `database/accounts/session_cache.go:39-40`、`:60-64` | `sessionCache.Lock()` + `defer Unlock()` 之后才做 JOIN 查询；token 由调用方控制（任意 `session_token` cookie，`web/api/principal.go:26`）。匿名垃圾 cookie 洪泛会**每条请求一次查询**并串行化所有会话查找。`internal/config/cache.go:15-26` 同样的形状，且在更热的配置路径上 |

## 7. P2 — 前端正确性与可用性

| # | 位置 | 问题 | 级别 |
|---|---|---|---|
| 7.1 | `frontend/src/pages/terminal/TerminalDialog.tsx:127-131` | **回车落在"取消"上会执行确认**：`onKeyDown` 挂在全屏 backdrop 上，`event.key === "Enter" && !isInput` 时 `preventDefault(); onSubmit()`；而"取消"按钮是它的子节点（`:233-235`），回车激活聚焦按钮是**该 keydown 的默认行为**，被冒泡阶段的 `preventDefault()` 抑制 → 只跑 `onSubmit()`。该组件被 `destructive` 用于**删文件**（`FileManagerPanel.tsx:1120-1140`、`FileEditorDialog.tsx:2045-2059`）。同一组件 `aria-modal="true"`（`:169`）却没有 Tab 陷阱，关闭后也不把焦点还给触发元素 | **HIGH** |
| 7.2 | `frontend/vite.config.ts:113`（`globIgnores` `:117-122` 只排除编辑器） | SW 预缓存把 258 个国旗 SVG（**273 KB gzip**）和 33 个 OS logo（57 KB）全量预取；当前 `dist/sw.js` 实测 451 条，其中 svg 284 条。每次 SW 安装/更新都要下这 ~330 KB，而 B1 的"全保留"结论从未给这部分定价 | MEDIUM |
| 7.3 | `frontend/src/pages/admin/exec.tsx:269-280` | 轮询 60 秒后**伪造** `finished_at: new Date().toISOString()` 与 `exit_code: -1` 写入 state 并停止轮询（`:151-161` 只清定时器）。70 秒才结束的命令永远不会被取回，界面把一个服务端从未上报的完成时间与真实 `exit_code` 并列显示 | MEDIUM |
| 7.4 | `frontend/src/lib/api.ts:55-73` | 保存设置后的迁移引导轮询：500 ms 间隔（`:31`）、自续期（`:69/:72`）、**无次数上限、无退避、无句柄、无取消路径**；面板重启期间标签页会以 2 Hz 永久请求 | MEDIUM |
| 7.5 | `frontend/src/pages/Index.tsx:170-172` | 公开首页的错误态是硬编码英文、无 `role="alert"`、无重试；`contexts/NodeListProvider.tsx:91-95` 还会在瞬时失败时 `setNodeList([])`——一次失败轮询就把内容换成裸错误文本 | MEDIUM |
| 7.6 | 35 处 `toLocaleString*`，如 `components/status/slaFormat.ts:37` | 时间戳一律按**浏览器本地时区**渲染且不标时区；唯一标了时区的地方是 `components/Footer.tsx:13-21`，它还把 locale 硬编码成 `zh-CN`。SLA 报告（H1）的卖点就是可分享 URL，而"故障起止"在每个查看者屏幕上都不一样 | MEDIUM-LOW |
| 7.7 | `frontend/src/pages/admin/index.tsx:43-49` | `filteredNodes` 内联 filter+sort，无 `useMemo` → 每次渲染新数组身份；`nodeTable/NodeTable.tsx:229-233` 按引用比较后渲染期 setState，于是**每 5 秒轮询多一轮渲染**，`SortableRow` 未 memo | LOW-MEDIUM |
| 7.8 | `components/Flag.tsx:75`、`utils/osImageHelper.ts:16-184`、`components/GuideHeader.tsx:12` | 绝对路径资源忽略可配置的 `base`；而 `vite.config.ts:72` 支持 `VITE_BASE_URL`、PWA manifest 也插值了它（`:93-107`）→ 子路径部署得到 404 的国旗/logo/安装图标 | MEDIUM-LOW |
| 7.9 | 见 §2.4 | `new Function` 执行表单文本 | MEDIUM |
| 7.10 | `frontend/src/i18n/config.ts:76` + 8 处 `dangerouslySetInnerHTML={{__html: t(...)}}` | `escapeValue: false` 的注释写"React handles XSS"，而这 8 处绕过了 React。当前 locale 值只是标记（`en.json` 有 5 个含标签的键），但 `frontend/crowdin.yml` 存在即接受外部贡献；`dompurify` 已在 `package.json:80-82` 里却**没有任何 import** | MEDIUM |

**测试盲区（MEDIUM，VERIFIED）**：`npm test` 的 129 条测试都是纯模块 + 源码文本断言
（例如 `script/oauth2-binding.test.mjs:71-94` 断言 `page.includes('oauth2BindUrl(')`），
**够不到任何管理写路径**——那些只在需要真实服务器的 Python spec 里（`ci.yml:117-120`）。
也就是说，"mutation 被报告成成功"这类本项目已经踩过的坑，无法让 `npm test` 变红。

## 8. P2 — 服务端健壮性

| # | 位置 | 问题 |
|---|---|---|
| 8.1 | `utils/geoip/geoip.go:62`、`:65` | `conf[config.GeoIpEnabledKey].(bool)` 未检查的类型断言。`config.GetMany` 返回**原始 JSON 解码类型**（`internal/config/config.go:93-99`），写入端不校验（`admin.misc.go:141-142` → `:180 config.SetMany`）。`InitGeoIp` 以 `go geoip.InitGeoIp()` 运行（`internal/server/providers.go:18`、`internal/server/runtime.go:80`）且无 recover → **后台 goroutine panic 直接结束进程**。一个类型写错的设置值能让面板在下次重载时崩溃（INFERRED 可达性） |
| 8.2 | `web/agent/v2_events.go:34-41` | `v2EventQueues` 按 client UUID 建条目，全仓库**没有删除**（只有 `:38` 建、`:209` 读）；同文件的 `connectedClients`/`v2Clients` 在 `web/agent/connections.go:74-75/:82-83` 有清理。条目按"历史上出现过的 client"增长，每条最多留 `v2EventQueueLimit = 128` 个事件 |
| 8.3 | `web/rpc/jsonrpc/admin.system.go:62-69`、`:81` | `admin:getLogs` 的 `limit` 只拒绝 `<= 0`，无上界；`:81 offset := (page-1)*limit` 可溢出。对比 `admin.dbquery.go:181` 有 `maxDBQueryLimit` |
| 8.4 | `database/clients/report.go:121` | `used > total` 时 `return fmt.Errorf("resource used value exceeds total")` 硬拒**整份上报** → 节点保持连接但数据冻结，甚至被判离线。应夹取并记一次日志 |
| 8.5 | `web/api/admin/theme.go:229`、`:214` | 主题解压用归档自带的 `f.FileInfo().Mode()`，而插件解压硬编码 `0644/0755`（`internal/plugin/install.go:139`）。同一类不可信输入，两套加固标准（路径穿越本身两边都挡住了） |
| 8.6 | `web/api/ws.go:55` | `IdentifyPrincipal(c)` 在 `for` 循环（`:40`）**内部**，每收到一条 WebSocket 消息就重读 cookie 并查一次 session（`web/api/principal.go:26-30`）。匿名访客的廉价消息循环即可放大成每帧一次 DB 查询（与 §6 的锁是同一把） |
| 8.7 | `internal/server/runtime.go:78-82` | 只在 `GeoIpProviderKey` 变化时重载 provider；`geo_ip_enabled` 只在 `InitGeoIp` 内被读（`utils/geoip/geoip.go:62`）。**运行中切换"启用 GeoIP"没有任何效果** |
| 8.8 | `agent/monitoring/netstatic/static.go:416-422` | 注释宣称存在"真实速率优先分支"，全仓库没有 `/sys/class/net/*/speed` 的读取；`:527-528` 的 `invalidDeltaLogged` 只置位不复位（P1-10 仍未修） |
| 8.9 | `agent/cmd/root.go:199` | 网卡白名单是启动快照（P1-11 仍未修）；`agent/monitoring/unit/net.go:508-531` 的前缀过滤先于 `--include-nics`，所以 br*/veth*/tap*/lo* 无法被包含（P1-12 仍未修） |
| 8.10 | `agent/cmd/token.go:126-129` + `agent/cmd/root.go:87-100` | 配置文件里的 `token` 先被反序列化进 `flags`，`resolveToken` 见 `flags.Token != ""` 即早返回 → **配置文件静默盖过 `-t`/`--token-file`**，而 `token.go:122-125` 的注释写的正好相反。用 `-t NEW` 轮换时旧 token 会获胜（P1-15 仍未修） |
| 8.11 | `utils/utils.go:7` + `:88/:98/:116/:147` | `DataMasking` 及其 Levenshtein 辅助链**无任何调用者**（含测试），且辅助函数未导出。约 150 行死代码，还会让人以为日志脱敏在这里（真正的脱敏是 `internal/metricstore/redact.go` 的正则） |

## 9. P3 — 文档与仓库卫生

### 9.1 CHANGELOG 有断层（VERIFIED）

`CHANGELOG.md` 里**没有** `v1.6.6`–`v1.6.10` 任何一个标题，也没有 `v0.1.44`–`v0.1.46`。
最后一条正式条目是 `:60` 的 `v1.6.5`。另外：

- 有两个 `## [Unreleased]`（`:34`、`:475`），其中 `:34` 那个装的是 **v1.6.10 的实际内容**——
  而 `4e7d389` 就是 tag `v1.6.10`，也就是说已发布的变更躺在"未发布"标题下；
- `v0.1.29`（`:9`）被放在**最前面**，在所有 `v1.6.x` 条目之上，文件不是最新在前。

后果：v1.6.9 是一个安全批次（P1-5/P1-6/P1-7/P1-13），在用户可见的变更记录里**不存在**，
记录只散落在 `docs/audits/2026-10-01/` 和 `docs/DEPLOY-OC424.md`。

### 9.2 ROADMAP 的状态表已过期（VERIFIED）

`docs/ROADMAP.md:17-37` 的状态表写着"Repository main `7ba066b`"、"Latest tag `v0.1.30`"、
"生产 `v0.1.30`"、"十台 agent 全 `v0.1.30`"。实际：HEAD = `v1.6.10`，生产 = `v1.6.9`。
文件自称最后修订 2026-09-26，之后落地了 10 个 tag。

同一份文件里更实质的错：
- `:184-191` 的批次表（H2→v0.1.32 … H6→v0.1.36）与本文件自己的收尾段落
  （`:271` H2=v0.1.36、`:320` H3=v0.1.37、`:374` H4=v0.1.38）以及 CHANGELOG 矛盾；H5 在 v0.1.39、H6 在 v0.1.42
  （`web/rpc/jsonrpc/admin.agentupdate.go:35` 的 `admin:rollAgentUpdate` 存在）；
- `:798-827` 说 D1"fleet not migrated"、"NOSLA 仍用 `-t`"、"九台里完成"——
  `docs/DEPLOY-OC424.md:671-685` 已更正："**NOSLA was never on `-t`** … No node is left on a command-line credential"；
- `:32/:829-837` 说非 root 是"两台"、把 NOSLA 当范例——实际 NOSLA 是 root（`DEPLOY-OC424.md:669`），
  只有 MAC-WAN 非 root，即 **9/10 仍是 root**；
- `:85-88/:107/:622` 引用的 `build-standalone-sla.mjs`、`vite.standalone.config.ts`、
  `web/public/standalone_test.go`、`frontend/script/_b1_panel.py` **都不存在**（实际文件名不同或已删）。

### 9.3 没有一份"活着的"开项台账（VERIFIED）

`ROADMAP.md:1083-1089` 把"完整开项清单"委托给
`docs/audits/2026-10-01/FINDINGS-STATUS-2026-10-01.md`——一份**冻结的 2026-10-01 快照**，
而它在那之后几天内就已经在 4 条上判错（P1-3 已由 `4e7d389` 修；P1-5/P1-6/P1-7 已由
`d5560cd`/`4552ed0`/`8d87458` 修；P1-13 已由 `79b4e72` 修）。
同时 `ROADMAP.md:17-37`、`OPEN-WORK.md:18`、`DEPLOY-OC424.md:6-27` **各自**还摆着一份过期的状态表。
建议：审计目录保持不可变的"证据"属性，但把**活的开项清单**收回 `OPEN-WORK.md`，
或在快照头部加"status as of + 后继链接"。

### 9.4 其它文档卫生问题（VERIFIED）

- **2 个坏链**（全部 .md 里只有这 2 个）：`docs/audits/2026-09-30/notes/README.md:14`（应为 `../../`）、
  `:15`（应为 `../../../`）。
- **孤儿文档**（无任何入站引用）：`docs/HEALTH-REVIEW-2026-09-19.md`、
  `docs/MONITORING-ACCURACY-REVIEW-2026-09-30.md`、`docs/OPTIMIZATION-RECOMMENDATIONS-2026-09-26.md`。
- **README 的文档索引只列了 ~40 份里的 14 份**，漏掉 ROADMAP、PERFORMANCE、AUTH-HARDENING、
  OAUTH-INTEGRATION、TRAFFIC-ACCOUNTING、AGENT-FOOTPRINT、RESOURCE-HARDENING、THIRD-PARTY-LICENSES
  以及全部 FOLLOWUP/review 和两个 `docs/audits/` 树——索引不是文档地图。
- **中英 README 已不一致**：`README.md:160` 给新文档加了索引行（英文索引），
  `README_zh-cn.md` 仍是 13 行 vs 14 行。
- **两份未提交产物互相矛盾**：`docs/OC424-MTPROTO.md:162-164` 把 `topology/` 站点描述为已部署，
  而 `topology/README.md:106-111` 说它当天上线当天撤下（410 Gone）。
- **`docs/audits/` 没有保留策略或责任人**：`docs/audits/2026-09-30/` 里含一个**变更生产**的脚本
  （`README.md:13` `deploy-oc424-v165.sh`）和三个无引用的复现转储。
- **`docs/audits/2026-09-30/notes/`** 自我声明已过期（`:16-18`）。

### 9.5 仓库卫生（多数是"可接受的旧账"）

| 项 | 状态 |
|---|---|
| `install-komari.sh`（45 KB，1464 行） | **无任何引用**（只有 `docs/HEALTH-REVIEW-2026-09-19.md:151` 提过），且它拉的是**上游**的 `komari-linux-*` 资产（`:59-61/:932/:953`）。`:1141-1145` 的 `local download_url=$(...)` 让 `if [ $? -ne 0 ]` 永远为假，错误分支是死的；全文没有 `set -euo pipefail` |
| `agent/.github/workflows/`（5 个 workflow） | 仍存在且仍引用前 fork 模块路径，但 GitHub 只读仓库根的 `.github/workflows`，所以是惰性的。`RELEASING.md:286-294` 说"已删除"，与事实不符 |
| `deploy/docker-compose.yml:3` | `image: ghcr.io/aone2233/nekomari:v0.1.14`——落后约 30 个版本、无 digest，而 `deploy/README.md:22` 称它是"The reference container setup" |
| `deploy/` 形态 | 顶层 75 个松散脚本 + `docs/`，有 `deploy/README.md` 索引，但扁平布局本身是维护成本 |
| 嵌入式主题归档不可复现 | `tools/zstdpack/main.go:105-111` 把 `fi.ModTime()` 打进 tar，`:52` 却声称"排序保证可复现的产物"；`-level` 在 13 以上是 no-op（`:157-168`）。结果是两次同源构建字节不同，5.39 MB 的 `web/public/defaultTheme/dist.tar.zst` 永远无法与重建结果比对 |
| 没有任何步骤断言归档与重建一致 | `ci.yml:208-217` 与 `release.yml:164-171` 跑 `script/embed-theme.mjs` 会**就地重写** `web/public/defaultTheme/*`，之后无人检查。改了 `frontend/` 却忘记提交归档的 PR 会通过 CI，而只用 `go build`/`go test` 的消费者拿到的是旧主题 |
| 三套二进制替换实现互不一致 | 安全的是 `deploy/fleet/upgrade-agent.sh:281`（`cp` → `mv`）+ `:227-229`（`mktemp -d` + trap）；不安全的是 `deploy/upgrade-agent.sh:73-79`（`cd /tmp`、固定文件名、无清理）与 `deploy/install-staged-agent.sh:62`。而 `deploy/README.md:28` 把**不安全**的那个写成升级路径。`DEPLOY-OC424.md:197` 自己记录过 `install` + 运行中 inode 会得 `ETXTBSY` |
| 未提交的工作 | `README.md` +1 行、`docs/OC424-MTPROTO.md`（164 行，内容完整、无待办）、`topology/`（11 文件 ≈300 KB，独立静态页）。`topology/` 自带脱敏检查器，我实跑过：`python tools/check-secrets.py` → **7 个文件，无 IP/UUID/凭据/token/ASN 命中，exit 0**。它按自己的 README 说"不接进 build.sh / Go 构建 / 文档站" |
| 测试覆盖 | 114 个含 `.go` 的目录里 **52 个没有 `_test.go`**。多数是 `pkg/jsruntime/*` 小包，但 `internal/config`（**零测试**，而它拥有 `GetAs`/`GetMany`/`Set` 和全部设置键）、`internal/lifecycle`、`internal/managedconfig`、`database/auditlog`、`database/records`、`database/notification` 都有实质逻辑 |
| 安全控制的测试缺口 | 没有任何测试枚举"哪些危险方法需要 step-up"（`grep MarkSensitive -- '*_test.go'` 无命中）——这正是 §2.2 能存在的原因；`web/api/admin/backup_metrics_test.go:21-38` 自建只含 `data/metrics.db` 的夹具并断言"不存在"，所以 §2.3 那种指向无人写入目录的白名单条目**永远不会让它失败** |

## 10. 建议的下一批（按顺序，含验收）

**批次 A — 安全收口（建议 v1.6.11，纯面板，不动 fleet）**

1. §2.1 匿名隐藏节点链：三处统一到 `clientHistoryReadable` / `unlock.ClientHistoryReadable`，
   hidden 与 unknown 回答逐字相同，`getPublicPingTasks` 过滤 hidden uuid。
   *验收*：一个真匿名 principal 依次调用三处，hidden 与 unknown 的响应**逐字节相同**；
   并新增一个"枚举所有已注册方法 + 断言 guest 可达名单"的测试（照 `dispatch_test.go:22` 的形状）。
   注意同时核对主题契约。
2. §2.2 `admin:dbQuery`/`dbExec` 补 `MarkSensitive` + 读审计。
   *验收*：一个已登录但未过 step-up 的 admin 会话调用两者都被拒；`MarkSensitive` 的名单有测试枚举。
3. §2.3 白名单拼写改为 `plugin-data/`。
   *验收*：装一个会写 `__storageDir__` 的插件 → 备份 → 恢复 → 数据仍在；
   并加一条"运行时写的每个 `./data` 子目录要么在白名单、要么被显式豁免"的测试。
4. §2.4 `new Function` 换成正则解析；§2.5 SSRF 默认改 `true`；§2.6 三处恒定时间比较。
   *验收*：`stringToBytes("1;(alert(1))")` 返回 0 且不执行；`ssrf_protection_enabled` 未设置时默认拒绝私网目标。

**批次 B — 发布链路（与 A 同批或紧随，改的是 workflow，不移动 fleet）**

5. §3.1 release 加 CI 门禁；§3.2 tag 正则收紧 + 两端 fail-open 改显式报错 + `build.sh` 版本注入 + 修正 FORK.md；
   §3.4 校验和改 `set -euo pipefail` + `>`；§3.3 dispatch 发布的 tag 解析。
   *验收*：在一个从未跑过 CI 的 commit 上打 tag，发布**被拒绝**；用 `v1.6.11-rc1` 打 tag，resolve 步骤**被拒绝**；
   `workflow_dispatch` 发布一次，`ghcr.io/.../nekomari:vX.Y.Z` 镜像存在。
6. §3.5 `agent/build_all.*` 路径改对 + `build.sh` 注入 agent 版本。
   *验收*：`bash agent/build_all.sh` 产出的二进制 `/api/version` 风格自报版本不是 `0.0.1`，且启动不触发自更新。
7. §3.6 发布验证加 arm64 腿。
   *验收*：`deploy-verify.sh` 在 arm64 上对 arm64 资产跑通。

**批次 C — 数字正确与门禁（面板侧）**

8. §4.1 配额告警与预测改用 `traffic.up`/`traffic.down`。
   *验收*：一台重启过的节点，其"本周期已用"在重启前后**连续**；预测在周期中段与月末对账。
   补一个"样本来自哪个序列"的测试——否则换错序列仍然全绿。
9. §5.1 修 `frontend/src/pages/sla/index.tsx:66-68` 并把 `audit:compiler` 接进 `ci.yml`；修正 ROADMAP A4。
   *验收*：`npm run audit:compiler` exit 0；把它从 CI 摘掉会让 CI 变红。
10. §5.2 i18n parity 测试 + 补齐 134×3 个键 + sync 改并集语义。
    *验收*：parity 测试在缺键时失败；`npm run i18n:sync`（无 key）**不删除**任何目标键。
11. §5.3 文档站检查接进 CI；修 `site/README.md` 对 `deploy/docs/build.sh` 的引用。§5.4 四个 action 脚本接进 `workflow-contracts`。

**批次 D — 数据安全与文档台账**

12. §6 四项（rollup 保留策略、`MigrationTargetKey`、prune 脚本 5 行、会话缓存双重检查锁）。
    *验收*：prune 脚本喂一个坏档仍能删掉过期备份；保留策略有"保留期短于层级跨度"的测试。
13. §9.3 建立一份**活的**开项台账（把 FINDINGS-STATUS 标为快照并指向它）；§9.1 补 CHANGELOG 的
    v1.6.6–v1.6.10 与 v0.1.44–v0.1.46；§9.2 更新 ROADMAP 状态表或把它降级为历史文档。
14. §9.5 里成本最低的几条：删 `install-komari.sh`、删 `agent/.github/workflows/`、
    修 `deploy/docker-compose.yml` 的陈旧 tag、`deploy/README.md:28` 指向 fleet 脚本、
    `tools/zstdpack` 的 `ModTime` 归零 + "归档与重建一致"的 CI 断言。

**批次 E — 前端正确性（可独立推进，与 A–D 无耦合）**

15. §7.1 TerminalDialog 回车（**这条最该先修**：它会删掉用户的文件）、§7.3 `exec.tsx` 伪造完成时间、
    §7.4 2 Hz 无取消轮询、§7.5 首页错误态、§7.7 表格重渲染、§7.2 预缓存国旗。

## 11. 已核实为"已修"——不要重复派活

这些在 `docs/audits/2026-10-01/FINDINGS-STATUS-2026-10-01.md` 里标着"未修/在途"，但代码已经是修好的：

| 条目 | 证据 |
|---|---|
| P1-3 首页 24h 流量放大约 286 倍 | `4e7d389` = v1.6.10；`frontend/src/utils/trafficSummary.ts` + `frontend/script/trafficSummary.test.mjs` |
| P1-5 匿名按 uuid 取出口 IP | `d5560cd`；`database/unlock/unlock.go` 的 `LoadVisible`/`ErrNotVisible` + `web/api/ipinfo/handler.go:225` |
| P1-6 慢≠丢包 | `4552ed0`（v1.6.9） |
| P1-7 OAuth 绕过 2FA | `8d87458`（v1.6.9），fail-closed |
| P1-13 agent 镜像缺容器标记 | `79b4e72`；`Dockerfile.agent:30` `RUN touch /.komari-agent-container` |
| P1-16 `pkg/jsruntime` 数据竞争 | `2e63569` |
| P1-18 `getMe` 回显 token | `f3839e3` |
| D1 agent token 出现在 `ps` | 十台全部迁完；`DEPLOY-OC424.md:671-685` 更正了"NOSLA 仍用 `-t`"的旧说法 |
| `deploy/docker-workflow-check.py` 未接 CI | 已接：`ci.yml:162`（`1933c0e`） |
| 上传锁争用（B2） | 生产实测 `busy: {}`，无争用可拆 |
| 服务 worker 预缓存策略（B1） | 已重新测量并决定"全保留"；**但见 §7.2**：国旗/logo 这部分从未被定价 |

## 12. 复核命令

```powershell
# 基线
git log -1 --format='%h %d %s'                       # 4e7d389 (= v1.6.10)
git status --short                                   # M README.md; ?? docs/OC424-MTPROTO.md; ?? topology/

# 健康度（本地实测全绿）
go build ./... ; go vet ./... ; go test ./... -count=1
cd frontend ; npx tsc --noEmit -p tsconfig.app.json ; npm run lint ; npm test

# §2.1 匿名可达性（静态证据链）
Select-String -Path web/rpc/jsonrpc/public.go -Pattern 'regPublic|Allow\('
Select-String -Path pkg/rpc/permission.go -Pattern 'Allow\("common'
Select-String -Path web/rpc/jsonrpc/dispatch.go -Pattern 'PrivateSiteKey'
Select-String -Path web/rpc/jsonrpc/public.go -Pattern 'Clients'          # :263 无过滤回显

# §2.2 step-up 名单只有一条
Get-ChildItem -Recurse -Filter *.go | Select-String 'MarkSensitive|IsSensitive'

# §2.3 拼写错误
Select-String -Path web/api/admin/backup_whitelist.go -Pattern 'data'
Select-String -Path internal/plugin/plugin.go -Pattern 'StorageDir'

# §2.4 任意 JS
Select-String -Path frontend/src/utils/unitHelper.ts -Pattern 'new Function'

# §3 发布链路
Select-String -Path .github/workflows/release.yml -Pattern 'needs:|workflow_run|sha256sum|grep -aq'
Select-String -Path .github/workflows/docker.yml  -Pattern 'head_branch|case "\$TAG"'
Select-String -Path agent/build_all.sh,agent/build_all.ps1 -Pattern 'ldflags'

# §5.1 编译器门禁（当前 exit 1）
cd frontend ; npm run audit:compiler ; echo "exit=$LASTEXITCODE"

# §5.2 i18n 缺口（展平后逐键比对；预期 id_ID/ja_JP/zh_TW 各缺 134）
cd frontend/src/i18n/locales
node -e "const fs=require('fs');const flat=(o,p='')=>Object.entries(o).flatMap(([k,v])=>v&&typeof v==='object'?flat(v,p+k+'.'):[[p+k,v]]);const en=Object.fromEntries(flat(JSON.parse(fs.readFileSync('en.json'))));for(const f of fs.readdirSync('.').filter(x=>x.endsWith('.json'))){const t=Object.fromEntries(flat(JSON.parse(fs.readFileSync(f))));console.log(f,Object.keys(t).length,'missing',Object.keys(en).filter(k=>!(k in t)).length)}"

# §5.3 / §5.4 没有流水线在跑的东西
Select-String -Path .github/workflows/*.yml -Pattern 'build.mjs|audit:compiler|check-action'
Test-Path deploy/docs/build.sh        # False —— site/README.md 引用了它

# §9.1 CHANGELOG 断层
Select-String -Path CHANGELOG.md -Pattern '^## ' | Select-Object -First 12

# §9.5 未引用的大文件
Get-ChildItem -Recurse -Include *.md,*.sh,*.yml,*.go | Select-String 'install-komari' -List
```

## 13. 本次未能核实

- **没有运行中的面板 / 数据库 / 网络**：所有运行时结论都是静态推理。没有跑 `go test -race`、profiler 或端到端 HTTP 探针。
- **没有浏览器**：ROADMAP B1 的 480 KiB 首访数字**无法从构建产物复现**（`dist/index.html` 实际引用
  `entry-index-*.js` 767 KB raw / 258 KB gzip 与 `index-*.css` 763 KB raw / 97 KB gzip，
  而文档说生产同两个文件是 31 KiB / 58 KiB），21 个 browser spec 也未执行。
  前端给出的数字是产物+gzip 测量，不是网络抓包。§7.2 的预缓存统计是从 `dist/sw.js` 数的。
- **没有屏幕阅读器、没有对比度计算**：§7.1 的无障碍结论是静态的（role/焦点/按键），不是 AT 实测。
- **真实 OAuth provider 登录**（ROADMAP A2）需要一台一次性实例。
- **MySQL/PostgreSQL 路径**：测试只覆盖 SQLite，方言相关 SQL 与 MySQL 索引迁移分支未在运行时验证。
- **`web/public/defaultTheme/dist.tar.zst` 是压缩二进制**，无法 grep，所以 §2.2 的"树内无调用者"只对
  Go 源码 + `frontend/src` 成立，不含编译后的主题包。
- **§6 的 rollup 触发条件**：内建指标默认保留 1 天，`RetentionDays == 0` 与非默认 policy 的组合取决于
  面板数据库里的实际配置，仓库内看不到 → 判为潜伏。
- **`topology/`** 未跟踪、无 Go 测试，只做了它自带脱敏检查器的实跑（干净）与 README 阅读。
- **线上事实**（SECRETS 事故 4 的管理员口令是否已改、Cloudflare 上的 `AAAA 100::` 记录、
  `topology.66040321.xyz` 的 A 记录、B3 的边缘路径）都需要生产访问。

## 14. 实施记录（2026-10-13 当天）与对本文的更正

第 10 节的批次 A–E 已全部落地，逐项改动与理由见 `CHANGELOG.md` 的 `[Unreleased]`。
这一节只记两件在实施过程中**推翻了本文判断**的事，以及一处实施中新发现的问题——
原文保留不动，更正写在这里，这样"当时怎么想的"和"后来发现什么"都留得住。

### 14.1 撤回：§6 的 P7-1「整层 rollup 被无条件删除」

**不是缺陷，是有意的设计，两条分支都有测试钉住。**

- `RetentionDays == 0` 不是"保留期没配"，而是**停用该指标**：`DeleteMetricDataIfDisabled`
  是配套的删除入口，`TestUpdateMetricRetentionDefersDisabledMetricCleanup` 与
  `pkg/metric/rollup_test.go` 里名为 `disabled` 的用例都覆盖了它。
- `all: true`（指标 policy 里没有的层）来自 `withMetricRetention` 的明确意图：
  "coarser tiers that do not extend retention are omitted because their buckets can be
  derived from the retained finer tier on demand"。`TestCleanupExpiredRemovesExpiredAndInvalidTiers`
  断言清理后只有 1 分钟层留有行，其余三层为空。

本文当时只读了 `store.go` 的清理逻辑，没有读它调用的策略函数和配套测试，把"一个零值
导致大范围删除"读成了隐患。**代码未改。**

### 14.2 撤回：§6 的 P7-4「恢复页覆盖 `MigrationTargetKey` 导致迁移自认源=目标」

**写这个键是对的；错的是它的注释比行为窄。**

`MigrationTargetKey` 记录的是"当前 metrics 目标指纹"，两处写入（迁移成功后、以及
store 配置被恢复/打开时）都在表达同一件事；`ResolveStoreMigrationSourceFingerprint`
把它当第二优先级，迁移完成后源与目标相等因而被拒绝——**那是有意的拒绝，不是静默搬自己**，
而且拒绝信息本来就不够可操作。

**改动**：`internal/metricstore/config.go` 的注释改成与行为一致；
`StartStoreMigration` 的拒绝信息现在说明"推断出的源等于目标，请显式传 source"；
新增 `store_migration_source_test.go` 钉住三档优先级与这条拒绝。

### 14.3 §9.5 的"归档不可复现"：ModTime 修好了，但**根因不是它**

`tools/zstdpack` 不再把文件 mtime 写进 tar 头（现在同一份 `frontend/dist` 连打两次字节相同）。
但按 §10 第 14 项的计划加"提交的归档 == 重建结果"的 CI 断言时发现：**重建一次前端再打包，
归档仍然不同**，所以这条断言**没有加**——加了会让每一次构建都变红。

逐条目对比两个归档后的结论（2026-10-13 实测）：

- 1062 个条目，逻辑文件名集合完全相同；
- 所有差异都只出现在**被引用的 chunk 文件名**上（`import ... from "./chunk-X-<hash>.js"`）；
- 唯一的**结构性**差异是 chunk 切分本身：同一个 `chunk-_layout.js` 一次是 1 个 import 的
  小块，另一次是 20+ 个 import 的大块；143 个 chunk 的名字哈希随之改变。

也就是说，不可复现的来源是 **rollup 的模块图遍历顺序**（Windows 上 `readdir` 不排序），
不是时间戳、不是内容。这已作为开项写进 `docs/OPEN-WORK.md`。归档里提交的那一份是权威的，
发布工作流会重建它；"归档的内容是不是面板自己的文档"由 `web/public` 下的 Go 测试断言
（那些测试不依赖字节可复现）。

### 14.4 实施中新发现：同一条泄露链上还有**第四处**

§2.1 列了三处（`public:getPublicPingTasks` 与两个判定器）。改的过程中在
`web/rpc/jsonrpc/common.record.go` 的 ping 分支里发现第四处：

```go
if params.UUID == "" && taskId != -1 {
    info["clients"] = t.Clients      // 原样回显，guest 可达
}
```

它和 `getPublicPingTasks` 泄露的是同一份数据，本文当时没看到。已一并修掉，并抽成
`visibleClientUUIDs`（纯函数，可单测）。

另外，本文 §2.4 顺带发现 `frontend/src/utils/unitHelper.ts` 的 JSDoc 示例值本身是错的
（`6,222,765 MB` 写成 `6525139624935`，实为 `6525042032640`）——一个从来没有人测过的函数，
连文档里的数字都没人核过。示例已更正，并补了 `frontend/script/unitHelper.test.mjs`。

### 14.5 批次 E（前端）的实施与实测

- **§7.1 回车落在「取消」上会执行确认**：判定抽成 `pages/terminal/dialogKeys.ts` 的
  `shouldSubmitOnEnter`，因为**没有 fixture 挂载 `TerminalDialog`**，而写一个跑不起来的
  fixture 等于没有测试。抽出来之后 `script/dialogKeys.test.mjs` 能在 `npm test` 里直接
  断言「来自 `<button>` 的 Enter 不是确认」—— 这条用例在修复前会红。
  同一处遗留 `aria-modal` 无 Tab 陷阱、关闭后不还焦点，已记为开项（需要先决定建议列表
  的焦点顺序）。
- **§7.2 预缓存国旗与图标**：实测（干净构建）**451 条 / 5257 KiB → 164 条 / 4449 KiB**，
  即约 287 个文件、约 800 KiB 从每次 SW 安装与更新里去掉。B1 那次测量定价的是首访
  480 KiB，从来不是这部分。离线可用性保留：`/assets/flags/`、`/assets/logo/` 走
  CacheFirst，用过即缓存。
- **§7.3 伪造完成时间**：只设一个 `polling_stopped` 标志，服务端的 `finished_at` /
  `exit_code` / `result` 一律不动，并给出「继续轮询」。客户端哨兵常量
  `TIMEOUT_RESULT_MARKER` 一并删除（全仓库确认服务端从不发送它）。
- **§7.4 2 Hz 轮询**：500 ms 起步、翻倍到 5 s 封顶、总预算 3 分钟、`pagehide` 时停止。
- **§7.5 首页错误态**：列表不再被清空；从未成功过时显示带 `role="alert"` 与重试按钮的
  翻译后错误，有数据时显示「显示的是上一次状态」的 `role="status"` 提示。
- **§7.6 时区**：`formatTime` 现在接受语言并附上时区缩写（每个时间戳自带，因为表格会
  分页、卡片会被单独截图）。SLA 事件列表已接入；其余约 33 处 `toLocaleString*` 记为开项，
  helper 已经在那里。
- **§7.7 表格重渲染**：`filteredNodes` 加 `useMemo`。

批次 E 的验证：`npx tsc -b` 0、`npm run lint` 0、`npm run audit:compiler` 0、
`npm test` **140/140**（改动前 129；新增 dialogKeys 3 条、i18n parity 4 条、unitHelper 4 条）。

### 14.6 实施后的验证证据

| 检查 | 结果 |
|---|---|
| `go build ./...` / `go vet ./...` | 0 / 0 |
| `go test ./... -count=1` | 全部 `ok`（含新增的 5 个测试文件） |
| `npx tsc -b` / `npm run lint` / `npm run audit:compiler` | 0 / 0 / **0**（改动前是 1） |
| `npm test` | 140/140（改动前 129；新增 i18n parity 4 条、unitHelper 4 条、dialogKeys 3 条） |
| `gofmt -l`（28 个改动过的 Go 文件，按 LF 归一化后） | 干净 |
| `bash -n`（6 个 shell 脚本） | 全部 0 |
| `python deploy/docker-workflow-check.py` | 通过（真值表 9 行，含新增的"手动发布"用例） |
| `python deploy/check-action-inputs.py` | 0 个问题（顺带证明所有 `uses:` 的大版本都存在） |
| `node site/script/build.mjs check` | 14 个页面全部存在 |
| 三个 workflow 的 YAML | 均可解析，`gate` 依赖关系正确 |

每个行为改动都做了**反向复核**（把修复撤掉，确认测试真的变红，再恢复）：
`secretcmp` 的门禁、备份白名单拼写、`audit:compiler`、i18n parity、预测的序列选择、
会话缓存的负缓存、prune 脚本、dispatch 发布的 tag 解析、agent 版本注入（实测前 fork 路径
确实静默不注入）。
