# 调研：设备「拉取 9router 模型失败」+ 「上游同步 Action 每日全败」

- 日期：2026-10-15 ｜ 状态：已定位根因（待决策修复方案）
- 设备：WiFi ADB `192.168.10.9:5555`（已连接实测）
- 结论一句话：**两个问题各自独立、都已定位到根因，均非"网络断了"这种不可控因素。**

---

## TL;DR

| # | 现象 | 根因 | 是否网络/代码问题 |
|---|---|---|---|
| 1 | 拉取 9router 模型获取不了 | `dnsfwd` 的 **5 个上游 DNS 全部不可达** → 对 `127.0.0.1:53` 一律回 SERVFAIL → 引擎解析不了任何域名 | **都不是**：是"上游配置陈旧/不可达"，网络与 dnsfwd 代码均正常 |
| 2 | 每日同步上游 Action 从没成功过 | 第 7 步 `Merge upstream baseline` **合并冲突熔断**（ADR-0008）：本仓从 v1.9.7 分叉，上游已到 v1.9.13，`git merge` 撞 **13 个冲突文件** | 是"分叉过大 + 重度定制共享文件"的结构性结果，熔断按设计触发 |

---

## 问题 1：拉取模型失败 = dnsfwd 上游全挂

### 架构前提（为何 DNS 一坏模型全灭）
- 引擎是纯 Go 静态二进制；Android 无 `/etc/resolv.conf`，Go 解析器读不到服务器列表时**只回落 `127.0.0.1:53`**（`module/service.sh`、`tools/dnsfwd.c` 头注释均实证）。
- 因此**引擎的全部出网解析都压在 `dnsfwd` 上**：模型目录（kiro/grok-cli/兼容节点的 live catalog）、各家 Provider API、更新检查，全部走它。DNS 一挂，这些全挂。
- `module/webroot/page-dns.js` 的 `dnsOff()` 注释早已写明这个因果：「关闭后 :53 无任何 DNS 服务——引擎域名解析会失败，模型将无法连接」。

### 实测证据（设备，2026-10-14 21:07 CST）
进程与监听：
- `dnsfwd`（pid 2507，监听 `127.0.0.1:53`，参数 `-f /data/adb/9router-go/dns-upstreams.conf -b loopback`）在跑；
- 引擎 `9router-go`（pid 2612）在跑。

`dnsfwd.log`（决定性）：
```
dnsfwd: 全部上游不可用（5 个），已回 SERVFAIL      ← 反复出现
dnsfwd: 队列已满（64），丢弃本次查询                ← 引擎重试洪泛把队列打满
```

`9router.log`：
- `GET /api/version/check ... duration=21.63s`（DNS 解析耗满超时）；
- updater 取 `SHA256SUMS.txt`（github）`context deadline exceeded (Client.Timeout ...)`。

### 已逐一排除的"其他可能"（都正常）
| 检查 | 结果 |
|---|---|
| 基础连通 | `ping 8.8.8.8 / 223.5.5.5 / 119.29.29.29` 全通（0% 丢包） |
| 系统解析器(netd) | `curl codebuddy` `dns=0.019s`，解析正常 |
| DoH | `curl dns.alidns.com/resolve` 返回正确 A 记录 `43.160.158.125` |
| dnsfwd 代码本身 | 我把 `bin/dnsfwd` 拷出，用**默认上游** `223.5.5.5`+`119.29.29.29` 跑 `-P` 探测：`2/2 ok`、RTT 3–4ms、答案正确 |
| 出网 UDP:53 | 同上——**未被网络封锁**（否则默认上游也测不出） |

**⇒ 唯一结论**：`dns-upstreams.conf` 里当前那 5 个上游，在**当前网络下全部不可达**（极可能是早前在别处网络做过「测速优选」选出来的，换网络后失效）。默认上游 `223.5.5.5`/`119.29.29.29` 此刻完全可用。

> 备注：`dns-upstreams.conf` 为 `600 root`，且被 SELinux 限制——`adb su`（`u:r:su:s0`）读不到，只有模块 WebUI 经 KernelSU `ksu.exec`（root、模块域）能读写。所以**不能**用 adb 直接改，必须走 WebUI（它自带 备份→写盘→热重载 的安全次序，见 `page-dns.js` 的 `DNS_STORE`）。

### 立即修复（操作，非代码）
模块 WebUI → **DNS 页** → 二选一：
1. **「恢复初始默认」**（恢复到 `223.5.5.5` + `119.29.29.29`，此刻实测可用）——最快；
2. **「测速优选」**（在当前网络重新实测 RTT/可用率，自动写 Top5 + 自定义项在前）。

两者都会 备份→写盘→`reload-dns` 热重载，无需重启引擎。

### 可选硬化（防止复发，需决策）
现状：当全部上游不可达时 `dnsfwd` 静默 SERVFAIL，守护（ADR-0004）只盯**进程存活**、不盯**解析健康**，于是"进程活着但解析全挂"这种半死态能长期存在。可选方案：给守护加一个"解析健康"探针（周期性解析一个已知域名，连续 N 次全败 → 自动把上游切回已知可用的默认集并热重载）。属新增能力，见下方决策。

---

## 问题 2：上游同步 Action 每日全败

### 实测证据（GitHub Actions）
- `sync-upstream.yml` 共 13 次运行，**#6–#13（2026-10-08 → 10-15）全部 `failure`**，均为每日 `schedule`；`main` 停在 `fe43d77`（2026-10-03）从未前进。
- 最新一次 run #13 的步骤结论：
  - 步骤 7 **`Merge upstream baseline` → failure**（约 3s）；
  - 步骤 8–11 → skipped；
  - 步骤 12 **`Open sync-failed issue (fuse)` → failure**。
- `sync-failed` issue 列表 = **空**（`[]`）。

### 根因 A：合并冲突熔断（ADR-0008 按设计触发）
本地复现（`git merge --no-commit --no-ff` 上游 tag 后 `--abort`，工作树无改动）：
- merge-base = `c1f5b6b` = **v1.9.7**（本仓分叉点）；上游最新 tag = **v1.9.13**（领先 6 个小版本：1.9.8→1.9.13）。
- 合并撞 **13 个冲突文件**，正是 AGENT-CONVENTIONS §10.2 那份"重度定制共享文件"清单：
  - 引擎：`internal/handlers/chat/types.go`、`internal/db/…`
  - 前端：`web/package.json`、`web/bun.lock`、`web/src/api/client.test.ts`、`web/src/components/ProfileSettingsView.svelte`、`web/src/components/analytics/AnalyticsView.svelte`
  - 元数据：`VERSION`、`version.json`、README.md`、`CHANGELOG.md`、`.gitignore`、`.github/workflows/…`

`sync-upstream.yml` 对 `git merge` 失败的处理就是 `git merge --abort && exit 1`（"熔断转人工"）——所以**只要分叉不缩，它每天都必败，且越拖越难合**。

### 根因 B（次级 bug）：熔断本应开的 issue 从未开出
- 仓库标签只有 GitHub 默认 11 个，**没有 `sync-failed` 标签**。
- 熔断步 `gh issue create --label sync-failed …` 因标签不存在而报错 → 步骤 12 失败 → **永远开不出 issue**。
- 后果：你从未收到"同步失败"通知，只在 Actions 页看到一片红。（这解释了"都是失败但没人告诉我"。）

### 附带发现
- **版本漂移**：`VERSION`=1.9.7、`module.prop`=v1.9.7-r2，但设备 `engine-version`=1.9.8、本地又有 `v1.9.8` tag——同步流程坏掉后版本号没对齐。
- **origin 陈旧**：CI 检出的是 `origin/main`=`fe43d77`（10-03），而本地 `main`=`8df9e8f`（领先约 108 个提交未推）。CI 在旧基线上做合并，本地成果没进流水线。

---

## 待决策（修复方向）

见聊天区的决策轮次。核心是三个岔路：
1. **设备 DNS**：立即走 WebUI 恢复/优选（操作层，推荐直接做）；是否要给守护加"解析健康自愈"（代码层）。
2. **同步策略**：自动 `git merge` 这条路在当前定制深度下走不通。是改成"只检测+通知、人工按 §7 合"，还是中期做"模块代码迁出共享文件"以降耦，需定方向。
3. **熔断通知**：补建 `sync-failed` 标签让熔断能真正开口子；并决定是否先把本地 `main` 推齐 `origin`。
