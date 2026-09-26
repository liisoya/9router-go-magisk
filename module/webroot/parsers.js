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
})(typeof self !== 'undefined' ? self : this, function () {
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
  const LIFECYCLE_STATES = {
    dns: {
      up:       { tone: 'ok',   text: pid => `运行中 (PID ${pid})` },
      disabled: { tone: 'err',  text: () => '已关闭（用户设置）' },
      yielded:  { tone: 'warn', text: () => '已让路（:53 被其他转发器占用）' },
      down:     { tone: 'err',  text: () => '未运行' },
    },
    engine: {
      up:      { tone: 'ok',   text: pid => `运行中 (PID ${pid})` },
      // 用户显式停服：守护尊重该意图，不会自动拉起 —— 所以文案与颜色都不是"故障"
      stopped: { tone: 'warn', text: () => '已停止（用户设置）' },
      down:    { tone: 'err',  text: () => '未运行' },
    },
    watchdog: {
      up:    { tone: 'ok',   text: pid => `运行中 (PID ${pid})` },
      stale: { tone: 'warn', text: () => '未运行（已武装，下次重启生效）' },
      down:  { tone: 'warn', text: () => '未启用' },
    },
  };
  const TONE_COLORS = { ok: 'var(--ok)', warn: 'var(--warn)', err: 'var(--err)' };

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
  // 之前""两个门禁都过才允许安装""快照失败不许删除"这些顺序不变量原先只活在 app.js 的装配流程里，
  // 离线没有任何断言，只能靠真机 T8b/T8d 兜（而 T8 只能在有设备时跑）。ADR-0007 的核心不变量
  // 因此长期处在"改 app.js 就可能悄悄破坏"的状态。
  // 做法：步骤顺序写成数据，planSteps 按序求值，遇到第一个未通过的门禁就停 ——
  // app.js 不再自己判断"顺序/是否允许执行"，只按求值结果执行；顺序不变量在离线有断言。
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

  // facts[id] = { ok, reason? }；**没给 fact 的门禁算未通过**（默认拒绝，不是默认放行）
  function planSteps(plan, facts) {
    const f = facts || {};
    const ran = [];
    for (const step of plan) {
      ran.push(step.id);
      if (!step.gate) continue;
      const v = f[step.id];
      if (!v || v.ok !== true) {
        return { ran, blockedBy: { id: step.id, reason: (v && v.reason) || `门禁 ${step.id} 未通过` } };
      }
    }
    return { ran, blockedBy: null };
  }

  return {
    stripCr, parseProp, cmpVer,
    parseOpsStatus, parseMeminfo, parseProcRss, FAKEIP_RE,
    parseDnsProbeLine, parseDnsProbeOutput,
    normUpstream, upType, UUID_ALIAS, extractAliases, computeOrphans,
    ELF_MAGIC, ENGINE_MIN_BYTES, engineFileGate, checksumGate,
    LIFECYCLE_STATES, TONE_COLORS, stateLabel,
    ENGINE_UPDATE_PLAN, MODULE_UPDATE_PLAN, ORPHAN_CLEAN_PLAN, planSteps
  };
});
