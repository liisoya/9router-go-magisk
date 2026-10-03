# 本机网络笔记：GitHub 可达性与代理取舍

> 记录实测结论，不是泛用教程。所有数字都是当时量出来的，**网络状态会变，用之前请重测**。
> 记录日期：2026-10-03（当晚复核过一次并修正了归因，见文末「修正记录」）。

## 一句话结论

**GitHub 只有前端 `github.com` 不可达，其余（`api` / `objects` / `codeload`）全部正常。**
所以：`gh` 命令全程可用；`git push` 会失败，重试能过；下载 release 资产要靠代理。

## 分 host 实测（PC，直连，各 4 次）

| Host | 可达 | 用途 |
|---|---|---|
| `github.com` | **0/4** | Web 前端、`git push` 的 smart-HTTP 端点 |
| `api.github.com` | 4/4 | `gh` CLI 全部走这里 |
| `objects.githubusercontent.com` | 4/4 | release 资产的实际下载源 |
| `codeload.github.com` | 4/4 | 源码包 tarball |

`github.com` 不可达时**不需要换节点**——换不动的是它自己。

## 由此推出的操作方式

```sh
# 推送：直连 + 重试。凭据只在这一次命令生效，不写入任何 git config。
git -c credential.helper='!gh auth git-credential' -c http.version=HTTP/1.1 \
    push https://github.com/<owner>/<repo>.git <branch-or-tag>
```

`git push` 是幂等的，中断后重推不会写坏任何东西，所以重试是安全的兜底。
实测 `main` 与 tag 都是第一次尝试就过。

## 下载：代理节点

release 资产在 `github.com` 上，所以下载要绕。节点列表见
<https://www.moretools.app/zh-CN/github-proxy>（纯前端转换器，页面声称不上传数据，
115 个节点且会轮换）。

**实测能用的**（拿真实 release 资产逐个试，**只认内容对得上**，`http=200`
但返回错误页的一律不算）：

| 节点 | 结果 |
|---|---|
| `https://gh.1k.ink/` | ✅ |
| `https://github.mlmle.cn/` | ✅ |
| `https://gh-proxy.com/` | ✅ |
| `https://gh.padao.fun/` | ✅（仓库 `fork`/`origin` 现用） |
| `https://ghproxy.monkeyray.net/` | ⚠️ 活着但不代理 release（404） |
| `gh-proxy.net` / `ghproxy.net` / `ghfast.top` / `ghproxy.cc` / `hub.gitmirror.com` / `ghproxy.cn` | ❌ 不可达 |

任何快照下来的清单都不该直接信 —— **包括这一份**。

## 下载 ≠ 推送：不要把 token 交给第三方

- **下载**（release / raw / archive）不涉凭据，节点随便用。
- **推送**会把请求转发给 GitHub，于是**你的 token 交给了那个第三方主机**。
  仓库的 `fork` / `origin` 就是 `https://<节点>/https://github.com/…` 这种形态。

用该形态推送且无可用凭据时，GitHub 会回
`Invalid username or token. Password authentication is not supported` ——
**这是 GitHub 的原话被镜像转述**，容易误判成自己 token 失效，实际是 git 根本没把
凭据交给那个主机（`gh` 的 credential helper 只服务 `github.com` 这个主机名）。

**结论：推送走直连 + 重试，不要为了推一个 commit 把 token 交出去。**

## 本机 mihomo：不要指望它

手机上有 mihomo（`/data/adb/box/mihomo`，`-f /data/adb/box/mihomo/config.yaml`），
但实测下来它对这件事**帮不上忙**：

- `tun.enable: false` —— 纯 proxy 模式，**不接管 9router 的出站**。
  9router 走的是 CMCC 直连，与 mihomo 无关。
- `mixed-port: 7890` 可用，但节点池实测**全军覆没**：`其他` 组当前节点
  `47.242.19.215:8899` 报 `i/o timeout`；逐个地区组测延迟（目标
  `www.codebuddy.ai`，超时 8s）：

  | 组 | 当前节点 | 结果 |
  |---|---|---|
  | 全部节点 / 自动选择 / 香港 | `🇭🇰HK_2\|2.7MB/s` | Timeout |
  | 台湾 | `[www.v2nodes.com] vless-TW-1…#5` | Timeout |
  | 日本 | `日本(yudou789.top 玉豆免费节点)` | Timeout |
  | 新加坡 | `🇸🇬 新加坡 \| SGP #1` | error |
  | 美国 | `US_speednode_0133` | Timeout |
  | 其它地区 | `🇮🇩 印尼 \| IDN #1` | error |

  节点还是**免费系且会变动**的（`2.7MB/s` 这类限速标签说明是订阅流量）。
  把 9router 的连接挂上去，等于把每次请求都塞进一个随时会变的死节点。

**所以：9router 不配代理。** 真要改出口，等哪天有稳定的自建节点再说，
并且优先用 mihomo 的 TUN（让路由由规则统一决定），而不是在 9router 里逐个连接配代理。

## 对本仓库的影响

- `update.json` 的 `zipUrl` / `changelog` 必须是**直连 GitHub** 的地址：
  它要进 release 资产、被别的设备当更新源用，走代理等于把代理可用性
  变成模块的可用性。
- CI（`release.yml`）跑在 GitHub 自己的 runner 上，与本机链路无关。
- 以上都只影响**本机人工操作**，不影响产物。

## 修正记录

初版把「`github.com` 不可达」归因为 *TLS 被中途掐断*（依据是一次
`GnuTLS recv error (-110)` 加 3 次里 1 次成功）。**这个归因是错的**，两点反证：

1. 分 host 复测后，`api` / `objects` / `codeload` 全部 4/4 正常，只有前端
   `github.com` 0/4 —— 若是链路层掐断，不会这么干净地按 host 分化。
2. 手机 mihomo 日志里出现过
   `192.168.10.28:60048 --> api.github.com:443 error: … i/o timeout`，
   即当时 PC 的部分流量**确实走了手机 mihomo**，
   `GnuTLS recv error` 很可能来自那条死节点路径，而非直连。

结论改为「按 host 分化 + 归因未定」，并据此重排了上面的操作方式。
写在这里而不是悄悄改掉，是因为那份错误归因已经推上去了。
