# ADR-0010: dnsfwd 解析健康自愈——守护新增「解析是否真的返回答案」判据

日期：2026-10-15 ｜ 状态：已接受

## 背景

引擎是纯 Go 静态二进制，Android 无 `/etc/resolv.conf`，Go 解析器读不到服务器列表时**只回落
`127.0.0.1:53`**（即 `dnsfwd`）。因此 DNS 一坏，模型目录（kiro/grok-cli/兼容节点 live catalog）、
各家 Provider API、更新检查**全灭**。2026-10-15 真机实证：`dnsfwd` 的 5 个上游全不可达 →
对 `127.0.0.1:53` 一律回 SERVFAIL → `/api/version/check` 耗时 21.6s、updater 取 github 超时、
用户侧"拉取 9router 模型获取不了"。（网络/系统解析器/DoH/dnsfwd 代码均已排除，纯是上游集失效。）

守护（ADR-0004）**只判进程存活、不判解析健康**——于是"进程活着但解析全挂"的半死态能长期潜伏，
用户看到的是"服务在跑却什么都取不到"，且无从判断。旧的"测速优选"上游换网络后失效是复发温床。

## 决策

1. **新增解析健康判据**（住在 `lib/lifecycle.sh`，ADR-0005 唯一所有者）：一个判据跑
   `dnsfwd -P` 探测当前上游集，回答"是否全部不可达 / 回 SERVFAIL"。**纯判定与 I/O 分离**——
   解析探测输出的判定抽成**纯函数**（可离线断言，同 `life_rss_log_due` 的纪律），实际跑探测与
   读写配置的 I/O 留在 lifecycle 的 I/O 函数；守护只调用谓词，不自己写第二份 DNS 健康实现（§2.1）。
2. **补救优先 reload，绝不重启**（ADR-0004）：连续 N 次判"全败"先 `life_reload_dns`（SIGHUP
   热重载）；仍败且确认上游集死 → 触发恢复动作。绝不因"解析不健康"重启引擎/dnsfwd（否则上游
   临时抖动会被放大成重启风暴）。
3. **恢复 = 自动重选可达上游**（用户 2026-10-15 决策）：用 `dnsfwd -P` 对「当前配置项 ∪ 内置
   默认集」实测，选出当前网络下**可达**的上游集；**备份优先**（先落 `.prev`/`.initial` 回滚点，
   与 `page-dns.js` 的 DNS_STORE 同语义）后原子写盘 + reload。安全网 = 既有「回滚上一版 /
   恢复初始默认」。
   - **单一所有者纪律（§2.1）**：shell 侧恢复**只做"可达性"这一个规则**（保留 `ok>0` 的上游），
     **不复制**面板 `page-dns.js` 的 RTT/可用率评分优选——紧急恢复与手动优选是不同操作，
     避免第二份评分实现。**完整的"测速优选"（候选池 + 评分）留待把候选池收敛为单一来源后的
     后续阶段**（见后果）。
4. **节流用秒**（§2.21）：探测与恢复都有秒级节流（仿 `life_rss_log_due`），配离线纯判定断言；
   探测放守护主循环**维护块**（缺省 ~60s，天然墙钟），**信号 handler 内零 fork**（§2.20）。
5. **全程如实记 `watchdog.log`**（探测结论、恢复动作、回滚点路径），不谎报成功。

## 被否决的替代

- **把"解析不健康"当重启判据**：拒绝（ADR-0004）——上游 502 / 临时抖动会被误判成该重启。
- **只告警不自动恢复**：拒绝——设备仍解析全挂，等于没修；用户明确要"遇到失败就自动优选"。
- **守护侧重写完整 RTT 评分优选**：拒绝——违反 §2.1 单一所有者（与 `page-dns.js` 的
  `parseDnsProbeOutput` 评分重复）。

## 后果

- "进程活着但解析全死"的半死态被守护自动识别并恢复，恢复用时 ≈ 连续失败门槛 × 探测周期。
- 守护新增对 DNS 的"动作"，但严格限定为 reload + 备份优先的配置恢复，**不碰进程生命周期**。
- **v1 与用户诉求的差异（需知情）**：v1 做的是"可达性重选"（当前 ∪ 内置默认），不是面板那种
  "候选池 + RTT 评分"的完整优选——已由文末 2026-10-22 修订落地（候选池 `dns-candidates`
  单一来源 + 评分公式下沉 dnsfwd + 周期自动优选）。
- 风险：自动改写上游配置——用 备份优先 + 可达性硬门槛 + 冷却 + 回滚点 兜住；用户手动
  「回滚上一版」仍为最终安全网。
- 需真机验收：构造"上游全死"场景，验证守护在 N 个周期内自动恢复解析、回滚点可用、
  恢复过程如实进 `watchdog.log`。离线已锁纯判定（`tools/test-lifecycle-lib.sh` L24，15 断言）。

## 修订（2026-10-15，v1.9.7-r3 落地）：探测目标改为"真实供应商主机"

原 v1 用写死的 `www.baidu.com` 做探测/治愈域名——错了：引擎真正要连的是**用户配置的供应商地址**，
且设备可能挂代理（mihomo 等）。本轮改为（用户决策 Q1–Q4）：

1. **探测/治愈目标 = DB 里的真实主机**：`life_dns_probe_hosts` 用模块 `sqlite3` 的 `json_extract`
   取活跃连接的 `providerSpecificData.baseUrl`（兼容节点=用户自设地址，如 `developer.amd.com.cn`），
   `life_dns_host_from_url` 提主机、去重、域名形状校验、上限 12。
2. **按连接的代理配置决定要不要 dnsfwd 代劳**（`life_dns_proxy_is_remote`）：代理做远端解析的
   （`socks5h://`/`http://`/`https://`）排除——那些由代理解析，测它们会误判；`socks5`/直连才需 dnsfwd。
   **不硬编码任何代理/域名**，设备有无代理都通用。
3. **首轮取代表**（前 4 个，= dnsfwd `-d` 上限），驱动自愈判定（快）；分轮探完全部做诊断完整性。
4. OAuth provider（无 baseUrl）的主机在引擎探针矩阵，shell 不复制（§2.1）→ 由后续**可达性诊断**
   （引擎侧、走每条连接自己的代理、只诊断建议不自动改）覆盖。

离线覆盖扩到 `tools/test-lifecycle-lib.sh` L24h/L24i（纯函数）+ **L25（fixture 库，验证 SQL 取主机 /
代理排除 / 去重）**；dash+bash 124/124。产物 `dist/9router-go-1.9.7-r3-magisk.zip`。


## 修订（2026-10-22，v1.9.14-r1 落地）：完整优选（候选池 + 评分 + 周期自动）

挂账的"候选池收敛 + 完整优选"本轮交付，形态与当初设想有一处偏差（更好的那种）：

1. **候选池单一来源 = `module/etc/dns-candidates`**（不是当初假设的并入 dns-defaults）：
   15 条国内明文/DoH/DoT 候选，`cat=`/`label=` 元数据与配置行同文件。面板芯片（page-dns.js
   运行时读）、手动优选（`ops.sh optimize-dns`）、守护自动优选（`life_dns_reoptimize`）
   三处共享；`tools/check-dns-candidates.sh`（DNSPOOL 门禁）防 JS 侧复活第二份全量清单。
2. **评分公式收敛进 dnsfwd 本体（`probe_score`）**：`-P --tsv` 新增末列 `score` =
   可用率×50 + 端到端握手率×30 + 速度×20，fake-ip 罚 100。当初担心的"shell 复制评分实现"
   以相反的方式解决——公式下沉到唯一会跑探测的二进制里，shell/JS 都消费 TSV 列，谁都不再
   持有公式。端到端维度（该上游给的答案 IP 能否对真实供应商主机 TCP 握手）直接对齐用户
   痛点（TLS 失败 = 答得出但连不通）。
3. **周期自动优选**（守护维护块，与自愈独立）：每 3 天（`LIFE_DNS_OPT_INTERVAL=259200`）
   对候选池∪当前配置同轮实测；候选最高分须超出当前集 15 分（`MIN_GAIN`）且连续 2 轮
   （`STRIKES`，计数落盘跨重启）才改写；探测期间用户编辑过配置即让位（cksum 快照**前移到
   探测之前**——快照在探测后等于没有窗口）；写盘 = 备份优先 + 自定义项保留在前 + Top5 +
   原子写 + 热重载；判据词（reoptimized / beaten-strike=N / not-beaten / stale-config /
   none-viable / …）如实进 watchdog.log。手动优选走同一条 `life_dns_reoptimize`（页面
   只发 `ops.sh optimize-dns` + 翻译判据词），紧急自愈（`life_dns_recover`）保持
   "可达性硬门槛"不变 —— 两者判据严格度不同（ok>0 即留 vs ≥50% + 评分门槛），不合并。
4. **转发态端到端沉底**（dnsfwd 转发路径）：对锚点主机的每次成功解析异步 TCP 握手一次，
   失败计 `tcpfails`（健康排序权重 ×3，成功查询减压）——自动优选之间的空窗里，坏上游
   也会在分钟级沉到队尾。dnsfwd 重编（GCC 15 aarch64 静态，WITH_DOH=1），与源码同步。
5. **离线覆盖**：`tools/test-dns-optimize.sh`（O1–O7，27 断言：评分对纯函数 / Top-N /
   候选池剥元数据 / reoptimize 全链桩测——双确认、让位、写盘形状、strike 清零 /
   TUN 绕行参数）；DNSPOOL 门禁 8 断言。
6. **真机验收（2026-10-22，Wi-Fi adb 192.168.10.10:5555，CMCC + mihomo TUN）**：
   两处只会在真机暴露的缺陷被当场抓到并修复——
   - **dnsfwd 读不了候选池**：`load_file` 把 `cat=明文` 当未知动词跳过，候选池文件
     直接 `-f` = "没有可用上游"（`none-viable` 根因）。修复：load_file 跳过
     `cat=`/`label=` token（评审同款格式因此成立：一份文件同时喂面板与 dnsfwd）。
   - **TUN 劫持探测**：mihomo `dns-hijack any:53` + auto-route 下，dnsfwd 探测的
     上游 socket 全部经 TUN → 死地址（TEST-NET 192.0.2.99）也答 2/2 100 分——
     自愈判"ok"、优选判"not-beaten"，整套判据失真（假阴性）。修复：
     `life_dns_tun_iface` 识别路由表里的 TUN 网卡（`meta`/`utun*`/`tun*`，从
     `ip route show table all` 的 `dev` 字段取——Android 的 default 散在
     per-network 表，main 表常常没有 default 行），探测自动追加
     `-i <物理网卡>`（SO_BINDTODEVICE 绕 TUN）；无 TUN 行为零变化。
   - 验收链路：装包 1.9.14-r1 → panel 109140/engine 1.9.14 → 注入死上游 →
     `life_dns_reoptimize` 返回 `reoptimized`（候选池评分 Top5 写盘 + 自定义项
     保留 + SIGHUP 重载 6 个上游 + 解析恢复）；`-i wlan0` 下候选池分层清晰
     （明文 100 分 / 域名型 DoT 需自举 0 分），探测数据恢复判别力。
   - 遗留观察项：mihomo 常驻场景下 TUN 期间"上游全死"的真实自愈（探测绕行后
     判据已真实，但 mihomo 本身也在兜底解析——两种健康的边界有待一次真实的
     上游全灭来标定）。

## 修订（2026-10-15，C3 后续阶段）：内置默认集收敛为单一来源

决策 3 挂账的"把候选池收敛为单一来源"分两步，本轮完成第一步（也是唯一有活跃
消费者的那一步）：

1. **内置默认集（223.5.5.5 + 119.29.29.29）收敛到 `module/etc/dns-defaults`**。
   它曾有四份拷贝：`life_prep` 的兜底文件、`life_dns_recover` 的候选腿（同一文件内
   两份字面量，注释自称"同源"却无任何缝合）、`dnsfwd.c` 的 `boot_resolve` 自举兜底、
   `page-dns.js` 候选池的一条腿。现在 lifecycle 两侧共用 `life_dns_default_lines()`
   （文件缺失时退硬编码最小集——保底不依赖新文件存在）；`tools/check-dns-defaults.sh`
   （DNSSRC 门禁）缝合文件形状 / fallback / dnsfwd.c 字面量 / 默认路径（顺手修 B10：
   g_conf 默认路径曾少 `-go`）。测试：L26（5 断言，行为打法同 L23）。
2. **候选池（原 page-dns.js 的 15 条菜单）已于 2026-10-22 迁出 JS**，收敛为
   `module/etc/dns-candidates` 单一来源（文末修订）——守护自动优选与手动优选成为
   第二、第三个消费者，当初"没有第二个消费者就不迁"的判断随之到期，迁出成本如预判
   很低（门禁与测试模式照搬）。
