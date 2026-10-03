# 本机网络笔记：GitHub 直连不稳时的下载与推送

> 记录实测结论，不是泛用教程。所有数字都是当时量出来的，过期了请重测。
> 记录日期：2026-10-03。

## 症状

直连 `github.com` 的 TLS 会被中途掐断（`GnuTLS recv error (-110)`），
`fetch` / `git push` 间歇性失败。**同一时刻 `gh` API 却是通的**
（`gh issue create` 成功），所以这不是"完全上不去"，是链路不稳。

实测（3 次连续）：

| 目标 | 结果 |
|---|---|
| 直连 `github.com` | **1/3 通**；取 release 资源直接返回空 |
| 镜像 `gh.padao.fun` | 3/3 稳 |
| `gh` API（api.github.com） | 正常 |

## 先分清两件事：下载 ≠ 推送

这是这份笔记里最要紧的一条。

- **下载**（release 资产 / raw / archive / 源码包）：把节点当 URL 前缀拼上即可，
  不涉及凭据，**放心用**。
- **推送**（`git push`）：节点会把请求转发给 GitHub，于是**你的 token 会被
  交给那个第三方主机**。仓库里的 `fork` / `origin` 远端就是这个形态：

  ```
  https://gh.padao.fun/https://github.com/liisoya/9router-go-magisk.git
  ```

  用这个形态推送时若没有可用凭据，GitHub 会回
  `Invalid username or token. Password authentication is not supported` ——
  这是 **GitHub 的原话**，只是被镜像转述了。

**所以：推送优先走直连 + 重试，不要为了推一个 commit 把 token 交给第三方。**

实测直连虽然只有 1/3 可用，但重试就能过 —— `git push` 本身是幂等的，
中断后重推不会写坏任何东西：

```sh
# 凭据只在这一次命令里生效，不写入任何 git config
git -c credential.helper='!gh auth git-credential' -c http.version=HTTP/1.1 \
    push https://github.com/<owner>/<repo>.git <branch-or-tag>
```

2026-10-03 实测：`main` 与 `v1.9.7-r2` 都是**第一次尝试就成功**，
前面几次失败只是 TLS 层没建起来。

## 实测可用的下载节点

对着一个真实 release 资产逐个试过（`v1.9.7-r1/changelog.md`），
**只认内容对得上**，`http=200` 但返回错误页的一律不算：

| 节点 | 结果 |
|---|---|
| `https://gh.1k.ink/` | ✅ 内容正确 |
| `https://github.mlmle.cn/` | ✅ 内容正确 |
| `https://gh-proxy.com/` | ✅ 内容正确 |
| `https://gh.padao.fun/` | ✅ 内容正确（仓库现用） |
| `https://ghproxy.monkeyray.net/` | ⚠️ 404（节点活着但不代理这个资源） |
| `gh-proxy.net` / `ghproxy.net` / `ghfast.top` / `ghproxy.cc` / `hub.gitmirror.com` / `ghproxy.cn` | ❌ 不可达 |

用法（把原 URL 整体拼在节点后面）：

```sh
curl -sS -m 20 "https://gh.1k.ink/https://github.com/<owner>/<repo>/releases/download/<tag>/<file>"
```

`http=200` 不代表可用 —— 一定要核对正文。另注意有的节点只代理 raw、
不代理 release，两者不是一回事。

## 完整节点清单

上面那几个会烂，清单本身也会烂。权威且实时的来源（115 个节点，会轮换）：

<https://www.moretools.app/zh-CN/github-proxy>

它是个纯前端的 URL 转换器（页面声称不上传任何数据），把 GitHub URL 粘进去
就出加速链接。**建议每次用之前自己重测一遍**，别直接信任何快照下来的清单 ——
包括这一份。

## 对本仓库的影响

- `update.json` 的 `zipUrl` / `changelog` 必须是**直连 GitHub** 的地址：
  它要进 release 资产、被别的设备当更新源用，走代理等于把代理可用性
  变成模块的可用性。
- CI（`release.yml`）跑在 GitHub 自己的 runner 上，与本机链路无关。
- 只有**本机开发时**会撞上这个问题，所以上面这些只影响人工操作，
  不影响产物。
