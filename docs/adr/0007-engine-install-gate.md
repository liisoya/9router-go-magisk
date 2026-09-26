# ADR-0007: 引擎更新采用"装前门禁 + fail-closed 校验"，版本号只在新引擎起来后才写

日期：2026-09-26 ｜ 状态：已接受（Phase 22 事故的架构结论）

## 背景

2026-09-26：模块 WebUI 的「下载并更新引擎」把 **9 字节的 HTTP 404 正文**（`Not Found`）
装成了引擎二进制，`engine-version` 同时被写成 `1.9.2`，`tools` 回滚用的 `.bak` 也被
同一份垃圾覆盖 —— 设备上再无可用引擎，引擎与面板一起停摆，守护每 21 秒"拉起失败"死循环。
唯一还活着的证据是内存里那个已经失去 exe 的旧进程，所以面板看起来"没更新成功"而不是"挂了"。

四层都缺校验，任何一层拦住都不会出事：

1. **下载器** `curl -sL`（没有 `-f`）：HTTP 404 的退出码仍是 0，正文被写出并 `echo dl-ok`；
2. **校验和**取不到时**跳过**校验继续安装（fail-open）——加速节点挂掉时正好没有校验和；
3. **`install-engine`** 不校验源文件本体（体积、ELF 魔数）就 `mv` + `chmod`；
4. **备份语义**是"替换前 cp 当前文件"：当前文件已损坏时，备份被一起污染（第二次尝试丢掉了唯一的好副本）。

## 决策

1. **下载器必须对 HTTP 错误失败**：`curl -fsSL`。"命令成功"不等于"内容正确"，但"命令失败"
   必须等于"中止"。
2. **装前门禁放在纯函数层**（`module/webroot/parsers.js`）：
   - `engineFileGate(size, magic)`：体积 ≥ 5MB 且文件头 `7f454c46`（ELF）；
   - `checksumGate(expected, actual)`：**取不到校验和即拒绝**（fail-closed）。
   两者是纯函数，可离线红绿回归 —— 夹具就是事故现场的 9 字节 `Not Found`。
3. **`ops.sh install-engine` 是唯一安装入口，且"先门禁、后动作"**：源不合格 →
   `install-rejected-src`，**不碰**现有二进制、不停服务、不写版本号（真机 T8b/T8d 断言）。
4. **回滚点只在"当前二进制本身合格"时才留**；新引擎起不来则自动回滚并报
   `install-failed-rolled-back`。
5. **`engine-version` 只在新引擎真的起来（`life_restart_engine` = `engine=up`）之后写**；
   回滚时保持旧值 —— 状态与事实绝不允许不一致（延续"不伪造版本"的既有原则，Phase 20 Q4）。
6. **`bin/9router-go.bak` 语义改为"最后一次已验证可用"**（只在成功后更新），
   不再是"替换前的当前文件"。
7. **「判据 vs 执行」的分工 ≠ 把规则抄两遍**（2026-09-26 架构候选 5 澄清）：
   规则（体积 ≥ 5MB 且文件头 `7f454c46`）的**声明只有一处** —— `parsers.js` 的
   `ELF_MAGIC` / `ENGINE_MIN_BYTES`；`ops.sh engine_src_ok` 是**执行前的复核**，
   不是第二份判据。两侧由 `module/webroot/test/engine-spec-contract.test.js` 缝死：
   常量值、魔数字面量、两项检查的存在性、边界语义（shell 的 `-ge` ↔ JS 的 `<`）任一处漂移即红。
   改规则 → 只改 `parsers.js`，再按门禁把 shell 那侧的常量同步过去。

## 后果

- 加速节点挂掉、资产改名、网络截断时，用户看到的是明确的"拒绝安装"，设备保持原样；
- 版本号与运行状态不再可能互相矛盾；
- 门禁集中在 `parsers.js`（判据）+ `ops.sh`（执行前）两处，可离线（node --test）与真机
  （`tools/device/test-lifecycle.sh` T8）双向回归；两处**同属一条规则**，由
  `module/webroot/test/engine-spec-contract.test.js` 缝住（决策 7）。
