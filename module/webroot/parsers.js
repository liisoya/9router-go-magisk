// @ts-check
/* parsers.js — 纯函数解析层（无 DOM / 无 shell 副作用）
 *
 * 深模块：把所有"环境差异敏感"的解析收敛到这里，接口是一组纯函数，
 * 同一文件在浏览器（window.KParsers）与桌面 node（module.exports）下都可加载，
 * 使解析层获得离线回归能力（node --test，fixture 来自真机实测输出）。
 * 历史教训：CRLF、sqlite3 box 模式、dnsfwd 空格对齐、/tmp 缺失 —— 全部在此层补偿。
 */
(function (root, factory) {
  if (typeof module === 'object' && module.exports) module.exports = factory();
  else root.KParsers = factory();
  // 类型闸说明：`this` 分支要转成 any —— 本文件带了 module.exports，TS 因而按 CJS 模块处理，
  // 顶层 `this` 的类型是"模块导出对象"，而浏览器分支要的是宿主全局。这是 UMD 的真实动态边界，
  // 在这里显式转换；工厂函数内部与跨文件引用仍然是**被检查**的（转换只覆盖这一处）。
})(typeof self !== 'undefined' ? self : /** @type {any} */ (this), function () {
  'use strict';

  // ── 通用 ──
  // ksu.exec / adb 输出可能带 \r，统一剥离后再解析
  function stripCr(s) { return String(s == null ? '' : s).replace(/\r/g, ''); }

  function parseProp(txt, key) {
    const line = stripCr(txt).split('\n').find(l => l.startsWith(key + '='));
    return line ? line.slice(key.length + 1).trim() : '';
  }

  // v1.9.1 与 v1.10.0 数值化比较：>0 表示 b 更新
  function cmpVer(a, b) {
    const pa = String(a || '').replace(/^v/, '').split('.').map(Number);
    const pb = String(b || '').replace(/^v/, '').split('.').map(Number);
    for (let i = 0; i < 3; i++) {
      if ((pb[i] || 0) > (pa[i] || 0)) return 1;
      if ((pb[i] || 0) < (pa[i] || 0)) return -1;
    }
    return 0;
  }

  // ── lib/ops.sh status 输出 ──
  // 兼容两种布局：单行空格分隔（promise 降级形态下 ops.sh 的输出形态）
  // 与多行 key=value（回调形态）。
  function parseOpsStatus(text) {
    const out = {};
    for (const tok of stripCr(text).split(/\s+/)) {
      const i = tok.indexOf('=');
      if (i > 0) out[tok.slice(0, i)] = tok.slice(i + 1);
    }
    return out;
  }

  // ── 生命周期状态词表：词 → 文案 + 严重度（单一所有者）──
  // `life_state`（module/lib/lifecycle.sh）是唯一 emit 方，它 emit 哪些词由那里的判定规则决定；
  // 这里给出"词 → 界面文案"的唯一映射，app.js 只查这张表（原先 app.js 里是三条 if/else 链）。
  // 为什么集中：同一套词表两侧各写一遍，加一个意图态时 app.js 会静默落到 else 显示"未运行"
  // —— 与键契约同源的静默漂移，只是漂的是**值**而不是键。门禁（test/contract-keys.test.js）
  // 把 shell 实际 emit 的词与这张表的键**双向**对齐，两侧任何一侧多/少一个词都会红。
  //
  // 四档严重度（界面底色直接用它）：
  //   ok  绿 = 正常在跑      off 灰 = **用户要它别跑**（退出，不是故障）
  //   warn 琥珀 = 需要留意     err 红 = 真故障
  // 2026-09-30：`dns.disabled` 原挂 err（把"用户关掉的"画成故障）、`engine.stopped`/`watchdog.down`
  // 原挂 warn —— 三处统一归到 off：它们都不是故障，界面不该报警。
  const LIFECYCLE_STATES = {
    dns: {
      up:       { tone: 'ok',   text: pid => `运行中 (PID ${pid})`, short: '运行中' },
      disabled: { tone: 'off',  text: () => '已关闭（用户设置）',   short: '已关闭' },
      yielded:  { tone: 'warn', text: () => '已让路（:53 被其他转发器占用）', short: '已让路' },
      down:     { tone: 'err',  text: () => '未运行',               short: '未运行' },
    },
    engine: {
      up:      { tone: 'ok',   text: pid => `运行中 (PID ${pid})`, short: '运行中' },
      // 用户显式停服：守护尊重该意图，不会自动拉起 —— 所以文案与颜色都不是"故障"
      stopped: { tone: 'off',  text: () => '已停止（用户设置）',   short: '已停止' },
      down:    { tone: 'err',  text: () => '未运行',               short: '未运行' },
    },
    watchdog: {
      up:    { tone: 'ok',   text: pid => `运行中 (PID ${pid})`, short: '运行中' },
      stale: { tone: 'warn', text: () => '未运行（已武装，下次重启生效）', short: '待重启' },
      down:  { tone: 'off',  text: () => '未启用',                short: '未启用' },
    },
  };
  const TONE_COLORS = {
    ok: 'var(--ok)', warn: 'var(--warn)', err: 'var(--err)', off: 'var(--dim-2)',
  };

  // 未登记的词**不冒充正常**（同"不伪造版本号"原则）：显式报未知并把原词透出来，
  // 让人一眼看到"shell 说了个界面不认识的词"，而不是安静地显示成"未运行"。
  function stateLabel(kind, value, pid) {
    const hit = (LIFECYCLE_STATES[kind] || {})[value];
    if (!hit) {
      return { text: `未知状态（${kind}=${value}）`, tone: 'err',
               color: TONE_COLORS.err, unknown: true };
    }
    return { text: hit.text(pid == null ? '' : pid), tone: hit.tone,
             color: TONE_COLORS[hit.tone], unknown: false };
  }

  /** 概览三块状态读数的**短形态**：底色表状态，数值只留 PID。
   *
   * 为什么另起一个函数而不是复用 stateLabel：那里的文案是给 DNS 页 kv 行用的完整句
   * （"运行中 (PID 101)"、"未运行（已武装，下次重启生效）"），三块并排时太长、显得乱。
   * 同一份状态知识需要两种呈现粒度 —— 粒度差异收在这一处，页面层不再自己拼字符串。
   *
   * 在跑时一律只给 PID（那才是用户想看的数字）；没在跑时给 short 短词。
   * 未登记的词与 stateLabel 同一条纪律：不冒充正常，报 '未知' 且挂 err。
   */
  function stateTile(kind, value, pid) {
    const hit = (LIFECYCLE_STATES[kind] || {})[value];
    if (!hit) return { tone: 'err', text: '未知' };
    if (value === 'up') return { tone: hit.tone, text: pid ? `PID ${pid}` : '运行中' };
    return { tone: hit.tone, text: hit.short };
  }

  /** RSS（kB）→ 面板文案。
   *
   * 唯一所有者：概览的引擎/DNS 读数牌与 DNS 页的状态牌都要显示内存，
   * 各写一份格式化就会漂（2026-10-01 架构评审 #2 收口时抽出）。
   * 0 / 读不到 → `-`：如实表示"没有可报告的内存"，不冒充 0 kB。
   */
  function fmtMem(kb) {
    const n = parseInt(kb, 10) || 0;
    return !n ? '-' : n >= 1024 ? (n / 1024).toFixed(1) + ' MB' : n + ' kB';
  }

  // 引擎版本"来源"文案（Phase 26 的派生状态自检）：告诉用户这个版本号是从哪读到的、
  // 是不是本次读取刚自愈过来的 —— 否则一旦面板照旧文件念（"假更新"观感），用户无从判断。
  function engineVersionSourceLabel(src, healed) {
    const base = ({ runtime: '运行期记录', package: '包内', none: '无来源' })[src] || `未知来源(${src})`;
    return healed ? `${base} · 刚自愈` : base;
  }

  /** 启动类动作的回执：是不是"本来就在跑"（幂等分支）。
   *
   * shell 侧 ensure_* / start-user 在"目标已在跑"时走幂等分支，回的是 `running`（dnsfwd）
   * 或 `engine=running`（服务），**不是** started / engine=up。界面若只认后者，就会把
   * "本来就在跑"判成失败、弹「❌ 启动失败」—— 而真相是它好端端跑着，用户读到"失败"
   * 会以为引擎/DNS 起不来（2026-10-01 真机）。两处页面各写一遍 `=== 'running'` 迟早会漂
   * （与键契约同源的静默漂移，只是漂的是值），判定收在这里；文案仍由调用方按自己语境给。
   */
  function isAlreadyRunning(got) {
    const s = stripCr(got).trim();
    return s === 'running' || s === 'engine=running';
  }

  // ── 动作回执词（ACTION_WORDS）：subcmd → 算"成功"的 shell 回词，唯一所有者 ──
  // 为什么它必须是数据（2026-10-01 架构走查候选 2）：expect 原先在每个调用点手抄（7+ 处），
  // shell 加一个幂等/失败词时门禁一条不会红、只能真机点按钮发现（dnsOn 只认 started、
  // start-user 泄漏第二行，两起事故面）。shell 侧对齐由 contract-keys 的词表门禁守着：
  // 表里出现 shell 不吐的词 → 红；动词的回词变了而表没认 → 红。
  // 未知动词不在表里 → actionOk 缺省拒绝（与 planGate 同一条纪律：静默放行比失败更坏）。
  const ACTION_WORDS = {
    'start-user':     { ok: ['engine=up', 'engine=running'] },
    'restart-engine': { ok: ['engine=up'] },
    'stop-user':      { ok: ['stopped'] },
    'enable-dns':     { ok: ['started', 'running'] },
    'stop-dns':       { ok: ['stopped'] },
    'install-engine': { ok: ['engine=up'] },
    'install-module': { ok: ['engine=up'] }
  };
  /** 判定一个动作回词是否算成功（trim 后全等命中成功词集之一）。runOpsAction 与
   *  手工路径（install 流程）共用这一个判定 —— "哪个词算成功"不许再有第二个家。 */
  function actionOk(subcmd, out) {
    const w = ACTION_WORDS[subcmd];
    const got = stripCr(out).trim();
    return !!w && w.ok.indexOf(got) !== -1;
  }

  // ── /proc/meminfo ──
  function parseMeminfo(text) {
    const mem = key => {
      const m = stripCr(text).match(new RegExp(key + ':\\s+(\\d+)'));
      return m ? parseInt(m[1], 10) : 0;
    };
    return { total: mem('MemTotal'), avail: mem('MemAvailable') };
  }

  // /proc/<pid>/status → VmRSS kB（找不到返回 null）
  function parseProcRss(text) {
    const m = stripCr(text).match(/VmRSS:\s+(\d+)/);
    return m ? parseInt(m[1], 10) : null;
  }

  // ── dnsfwd -P 输出 ──
  // 实测为空格对齐（无 tab），upstream 本身可含空格（doh/dot 前缀）：
  //   223.5.5.5                v4  www.baidu.com   28ms  2/2  答案...
  //   doh https://x/dns-query  doh www.baidu.com  455ms  2/2  答案...
  // 从行尾锚定解析，rtt 带 ms 后缀。
  const PROBE_RE = /^(.+?)\s+(v4|v6|doh|dot)\s+(\S+)\s+([\d.]+)ms\s+(\d+)\/(\d+)\s*(.*)$/;
  function parseDnsProbeLine(line) {
    const m = stripCr(line).trim().match(PROBE_RE);
    if (!m) return null;
    const okTotal = parseInt(m[5], 10), all = parseInt(m[6], 10) || 1;
    const okRate = okTotal / all;
    return {
      upstream: m[1].trim(),
      family: m[2],
      domain: m[3],
      rtt: parseFloat(m[4]) || 9999,
      okRate,
      answers: m[7] || ''
    };
  }
  // 行数组 → 合格候选（可用率 ≥50%），按评分降序。
  // 评分 = 可用率×100 − RTT/50 − fake-ip 罚 100（与 index.html 的承诺一致）。
  // fake-ip 判据来自 dnsfwd -P 人类可读输出行尾的 `← fake-ip(TUN 接管)`（tools/dnsfwd.c:1368，
  // 答案落在 198.18.0.0/15 = mihomo 默认 fake-ip 段时追加）：走 TUN 假 IP 的上游虽然 ping 得通，
  // 但给不了真实解析，必须重罚，否则优选会把假 IP 的上游排到前面。
  const FAKEIP_RE = /fake-ip/;
  function parseDnsProbeOutput(text) {
    const rows = [];
    for (const line of stripCr(text).split('\n')) {
      const p = parseDnsProbeLine(line);
      if (!p || p.okRate < 0.5) continue;
      const fakeip = FAKEIP_RE.test(p.answers);
      rows.push({
        upstream: p.upstream, rtt: p.rtt, okRate: p.okRate, fakeip,
        score: p.okRate * 100 - p.rtt / 50 - (fakeip ? 100 : 0)
      });
    }
    rows.sort((a, b) => b.score - a.score);
    return rows;
  }

  // ── DNS 上游行 ──
  // 统一为 dnsfwd 可读形式：nameserver IP / doh URL / dot host
  function normUpstream(line) {
    const t = String(line == null ? '' : line).trim();
    if (!t || t.startsWith('#')) return t;
    if (/^(nameserver|doh|dot)\s/.test(t)) return t;
    if (/^https:\/\//.test(t)) return 'doh ' + t;
    if (/^tls:\/\//.test(t)) return 'dot ' + t.slice(6);
    return 'nameserver ' + t;
  }
  function upType(line) {
    const t = String(line || '').trim();
    if (/^doh\s/.test(t) || /^https:\/\//.test(t)) return 'DoH';
    if (/^dot\s/.test(t) || /^tls:\/\//.test(t)) return 'DoT';
    return '明文';
  }

  // ── 孤儿判定（结构性安全）──
  // 只有 openai-compatible-chat-<uuid> 形状且 UUID 不在存活集合里的别名才可清理。
  // 内置供应商别名（oc/ds/qd/cd/or/cbai…）来自引擎内置注册表，DB 里查不到是正常的，
  // 绝不能因此判定为孤儿（曾误删 oc/qd 的自定义模型）。
  const UUID_ALIAS = /^openai-compatible-chat-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
  // kv 行（customModels/disabledModels 的 key）→ 别名集合
  function extractAliases(lines) {
    const set = new Set();
    for (const raw of lines) {
      const l = stripCr(raw).trim();
      if (!l || !l.includes('|')) continue;
      set.add(l.split('|')[0]);
    }
    return set;
  }
  function computeOrphans(aliases, liveSet) {
    return [...aliases].filter(a => a && UUID_ALIAS.test(a) && !liveSet.has(a));
  }

  // ── 引擎更新：装前门禁（2026-09-26 事故）──
  // 事故：加速节点对 release 资产返回 404，`curl`（当时没有 -f）把 9 字节正文
  // "Not Found" 当二进制装上，engine-version 还写成了 1.9.2 —— 引擎直接起不来，
  // 备份也被同一份垃圾覆盖。判据必须是"这像不像一个引擎"，而不是"下载命令报没报错"。
  const ELF_MAGIC = '7f454c46';
  const ENGINE_MIN_BYTES = 5 * 1024 * 1024; // 真实产物约 25MB；5MB 下限足以挡住文本/HTML/截断
  function engineFileGate(size, magic) {
    const n = Number(size);
    if (!Number.isFinite(n) || n <= 0) return { ok: false, reason: `引擎文件读不到或为空（size=${size}）` };
    if (n < ENGINE_MIN_BYTES) return { ok: false, reason: `下载物只有 ${n} 字节（引擎约 25MB），不是引擎二进制` };
    if (String(magic || '').toLowerCase() !== ELF_MAGIC) {
      return { ok: false, reason: `文件头不是 ELF（${magic || '空'}），拒绝安装` };
    }
    return { ok: true, reason: '' };
  }
  // 校验和口：expected 取不到即拒绝（fail-closed）。
  // 旧实现是"取不到就跳过校验继续装" —— 那正好在加速节点挂掉时放行了 404 正文。
  function checksumGate(expected, actual) {
    const e = String(expected || '').trim().toLowerCase();
    const a = String(actual || '').trim().toLowerCase();
    if (!/^[0-9a-f]{64}$/.test(e)) return { ok: false, reason: '未取到 SHA256SUMS（拿不到校验和就不装）' };
    if (e !== a) return { ok: false, reason: `SHA256 不匹配（实际 ${a || '空'}，期望 ${e.slice(0, 12)}…）` };
    return { ok: true, reason: '' };
  }

  // ── "先门禁后动作"的计划：顺序是数据，可离线断言 ──
  // 为什么：门禁本身是纯函数、有红绿用例，但**会出事的是怎么被调用** —— "门禁必须在 install-engine
  // 之前""两个门禁都过才允许安装""快照失败不许删除"这些顺序不变量原先只活在装配流程里，
  // 离线没有任何断言，只能靠真机 T8b/T8d 兜（而 T8 只能在有设备时跑）。ADR-0007 的核心不变量
  // 因此长期处在"改调用点就可能悄悄破坏"的状态。
  // 做法（2026-09-30 C1 重整）：计划常量仍是**顺序与门禁的唯一声明**，运行时由各调用点按计划顺序
  // 分阶段调用 planGate（每次只持有本阶段 fact），门禁不过即 return；
  //   · **顺序**由流程用例守（gate-flows/orphan-scan 断言真实命令序列）；
  //   · **结构**由 parsers.test.js 守（门禁位置 + 调用点阶段名必须存在于计划且是门禁）。
  const ENGINE_UPDATE_PLAN = [
    { id: 'download' },
    { id: 'file-gate', gate: true },   // 像不像一个引擎（体积 + ELF 魔数）
    { id: 'sum-gate', gate: true },    // SHA256（取不到即拒绝）
    { id: 'install' },                 // 唯一安装入口（ops.sh install-engine）
  ];
  const MODULE_UPDATE_PLAN = [
    { id: 'download' },
    { id: 'zip-gate', gate: true },    // zip 里必须有 module.prop
    { id: 'install' },                 // ops.sh install-module
  ];
  const ORPHAN_CLEAN_PLAN = [
    { id: 'scan', gate: true },        // 扫描结果不可信（撞引擎写事务）→ 不得进入判定
    { id: 'recheck', gate: true },     // 删除前逐项复查：仍存活则从清理清单剔除
    { id: 'snapshot', gate: true },    // 删除前快照失败 → 不得删除（安全优先）
    { id: 'delete' },
  ];
  const DNS_OPTIMIZE_PLAN = [
    { id: 'probe' },                   // 跑测速（有副作用：写临时候选文件）
    { id: 'rows-gate', gate: true },   // 没有任何可用上游 → 不得改写配置
    { id: 'backup' },                  // 写配置前留回滚点
    { id: 'backup-gate', gate: true }, // 回滚点不可用 → 不得改写（否则用户配置无可回滚）
    { id: 'write' },                   // 原子写入新配置
    { id: 'reload' },                  // 热重载 dnsfwd
  ];

  // facts[id] = { ok, reason? }
  //
  // 这里曾有一个 `planSteps`（按计划顺序 walk、遇未通过门禁即停）。2026-09-30 架构扫描 C1
  // 把它删了：它**生产零调用**（生产只用下面的 planGate），只有测试引用 —— 于是那些测试在测一个
  // 退役的求值器（假信心），而它又给人"顺序由计划保证"的错觉。**顺序的真实保障**是：
  //   · 各调用点按计划顺序 await（顺序写在流程里）；
  //   · gate-flows.test.js / orphan-scan.test.js 断言**真实命令序列**（这才是会红的顺序门禁）；
  //   · parsers.test.js 锁"计划结构"与"调用点阶段名必须存在于计划且是门禁"（防拼错被静默跳过）。
  // 按阶段求值：只求值 id === phase 的那一道门禁。
  // 为什么存在：调用方在每个阶段只持有该阶段的 fact，而"缺 fact = 拒绝"的整计划求值
  // 同构事故（2026-09-28，其中两个是走查发现的活体）全部源于此。
  // 跨阶段顺序仍由唯一计划常量承载：各阶段按计划顺序各自调 planGate，顺序不变量
  // 不回到调用方手里。
  function planGate(plan, phase, fact) {
    const step = (plan || []).find(s => s.id === phase);
    // 未知阶段名**必须拒绝**（2026-10-01 架构评审 #6）。原先 `!step` 与「非门禁阶段」一起放行，
    // 于是**阶段名拼错 = 门禁静默失效**（fail-open）—— 一个永不拦人的门禁比没有更坏，
    // 因为它会被当成"已经守住了"。拼错属于计划与调用点不一致，是必须红的形状。
    if (!step) {
      return { ok: false, reason: `计划里没有阶段「${phase}」（阶段名拼错，或计划改了没同步调用点）` };
    }
    if (!step.gate) return { ok: true, reason: '' };  // 非门禁阶段不拦
    if (!fact || fact.ok !== true) {
      return { ok: false, reason: (fact && fact.reason) || `门禁 ${phase} 未通过` };
    }
    return { ok: true, reason: '' };
  }

  // ── 孤儿扫描输出 → { live, aliases }（纯函数）──
  // 无 '|' 行 = 存活（providerNodes.id / providerConnections.provider）；
  // 有 '|' 行的首段 = 模型别名。fixture 可离线断言（曾因执行层标记行无 '|'
  // 被误算进存活 —— 环境噪音必须由产出方剥净，这里只认干净输出）。
  function parseScanLines(lines) {
    const live = new Set(), aliases = extractAliases(lines);
    for (const raw of lines) {
      const l = stripCr(raw).trim();
      if (l && !l.includes('|')) live.add(l);
    }
    return { live, aliases };
  }

  // ── 凭据扫描输出 → 缺凭据的活跃连接（纯函数）──
  // 行形如 id|provider|authType|apiKey|accessToken|refreshToken（COALESCE 成 'null'）
  function parseCredScan(text) {
    const rows = [];
    for (const line of stripCr(text).split('\n').map(s => s.trim()).filter(Boolean)) {
      const p = line.split('|');
      if (p.length < 6) continue;
      const [, provider, authType, apiKey, accessToken, refreshToken] = p;
      const hasKey = apiKey && apiKey !== 'null';
      const hasTok = (accessToken && accessToken !== 'null') || (refreshToken && refreshToken !== 'null');
      if (!(authType === 'oauth' ? hasTok : hasKey)) rows.push({ provider, authType });
    }
    return rows;
  }

  // ── 批量测速输出 → [{ i, node, ok, ms, err }]（纯函数）──
  // 行形如 `3\thttps://x/\t200 0.123` 或失败时 `3\thttps://x/\t000 3.001 Could not resolve host: …`
  // （索引 / 节点 / curl 的 -w 结果三段，制表符分隔；第三段是 `<http_code> <time_total> [errormsg]`）。
  // **必须带索引**：`cat *.out` 的 glob 是字典序（tag-10 会排在 tag-2 前面），
  // 靠行序映射回节点会错位 —— 所以索引写在行里，这里按索引排序还原。
  // 失败原因（err）带出来给界面显示：只说"不可用"没法排查，说了"resolve/超时/证书"才有用。
  function parseCurlTimings(text) {
    const rows = [];
    for (const line of stripCr(text).split('\n')) {
      if (!line.trim()) continue;
      const parts = line.split('\t');
      if (parts.length < 3) continue;
      const i = parseInt(parts[0], 10);
      const node = parts[1];
      if (!Number.isFinite(i) || !node) continue;
      const m = parts[2].trim().match(/^(\d{3})\s+([0-9.]+)\s*(.*)$/);
      rows.push({
        i, node,
        ok: !!(m && m[1] === '200'),
        ms: m ? parseFloat(m[2]) * 1000 : 0,
        err: m ? (m[3] || '').trim() : ''
      });
    }
    rows.sort((a, b) => a.i - b.i);
    return rows;
  }

  return {
    stripCr, parseProp, cmpVer,
    parseOpsStatus, parseMeminfo, parseProcRss, FAKEIP_RE,
    parseDnsProbeLine, parseDnsProbeOutput,
    normUpstream, upType, UUID_ALIAS, extractAliases, computeOrphans,
    ELF_MAGIC, ENGINE_MIN_BYTES, engineFileGate, checksumGate,
    LIFECYCLE_STATES, TONE_COLORS, stateLabel, stateTile, fmtMem, engineVersionSourceLabel, isAlreadyRunning,
    ACTION_WORDS, actionOk,
    ENGINE_UPDATE_PLAN, MODULE_UPDATE_PLAN, ORPHAN_CLEAN_PLAN, DNS_OPTIMIZE_PLAN, planGate,
    parseScanLines, parseCredScan, parseCurlTimings,
  };
});
