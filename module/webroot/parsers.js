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

  return {
    stripCr, parseProp, cmpVer,
    parseOpsStatus, parseMeminfo, parseProcRss, FAKEIP_RE,
    parseDnsProbeLine, parseDnsProbeOutput,
    normUpstream, upType, UUID_ALIAS, extractAliases, computeOrphans,
    ELF_MAGIC, ENGINE_MIN_BYTES, engineFileGate, checksumGate
  };
});
