/* bridge.js — root-shell 资源访问层（KSU ksu.exec 桥）
 *
 * 深模块：环境差异与命令拼装全部补偿/收敛在这里 ——
 *   1. 调用形态自适应探测：各管理器（KernelSU 版本 / WebUIX）对 ksu.exec 的
 *      支持差异极大（3 参回调 / 2 参回调 / Promise）。启动时发送带标记的
 *      echo 探测，哪种形态能收回输出就用哪种，不赌。
 *   2. 哨兵式完成判定：回调形态累积全部输出块，以 `echo __KMOD_DONE__<$?`
 *      哨兵判定结束并携带退出码（流式多块回调的管理器上，Promise 形式
 *      只会保留最后一个块——曾导致多行输出只剩末行：版本"未知"、资源"-"、
 *      孤儿扫描只见 1 项）。
 *   3. 全局串行队列（并发 ksu.exec 在部分构建上输出交叉/丢失）
 *   4. CRLF 剥离；SQL 经 $DATA_DIR/cc.sql 临时文件执行（Android 无 /tmp；
 *      -list 强制管道输出）；database is locked 自动重试（引擎与 WebUI 共库）
 *   5. 命名操作层（Phase 2）：readFile / writeFile / download / … 的命令拼装
 *      与引号转义（shq）只有这一个家，app.js 不再内联拼 shell。构造器是纯
 *      函数（_cmds 导出），离线可测（test/bridge-commands.test.js）。
 *
 * 依赖：index.html 先注入 window.CFG = { MODDIR, DATA_DIR }，再加载本文件。
 */
(function (root, factory) {
  if (typeof module === 'object' && module.exports) module.exports = factory();
  else root.KBridge = factory();
})(typeof self !== 'undefined' ? self : this, function () {
  'use strict';

  let _chain = Promise.resolve();
  let _mode = null; // 探测到的可用形态：cb3 / cb2 / promise

  const SENTINEL = '__KMOD_DONE__';

  function cfg() { return (typeof window !== 'undefined' && window.CFG) || {}; }

  // ── 引号转义：所有拼进命令的路径/URL/内容都经此处理（唯一出口）──
  function shq(s) {
    return "'" + String(s == null ? '' : s).replace(/'/g, "'\\''") + "'";
  }

  // ── UTF-8 安全的 base64（writeFile 内容通道 / panel 的 upstreams_b64 解码）──
  function b64Encode(s) {
    const bytes = new TextEncoder().encode(String(s == null ? '' : s));
    let bin = '';
    for (const b of bytes) bin += String.fromCharCode(b);
    return btoa(bin);
  }
  function b64Decode(b64) {
    if (!b64) return '';
    try {
      const bin = atob(b64);
      return new TextDecoder().decode(Uint8Array.from(bin, c => c.charCodeAt(0)));
    } catch { return ''; }
  }

  // ── 回调块累积（sentinelExec 与形态探测共用，曾因复制两份而漂移）──
  function appendChunk(acc, chunk) {
    if (typeof chunk === 'string') return acc + chunk;
    if (chunk && typeof chunk.stdout === 'string') return acc + chunk.stdout;
    return acc;
  }

  // 按形态发起调用（不管理完成判定）；未知形态显式报错，不静默返回 undefined
  function callForm(mode, cmd, cbName) {
    if (mode === 'cb3') return ksu.exec(cmd, '{}', cbName);
    if (mode === 'cb2') return ksu.exec(cmd, cbName);
    if (mode === 'promise') return ksu.exec(cmd);
    throw new Error('unknown ksu.exec form: ' + mode);
  }

  // 哨兵式执行（回调形态）：累积全部输出块直到哨兵出现
  function sentinelExec(mode, cmd, cbName, timeoutMs) {
    return new Promise(resolve => {
      let out = '', settled = false;
      const finish = r => {
        if (settled) return;
        settled = true;
        delete window[cbName];
        clearTimeout(timer);
        resolve(r);
      };
      window[cbName] = function (chunk) {
        if (settled) return;
        out = appendChunk(out, chunk);
        const i = out.indexOf(SENTINEL);
        if (i !== -1) {
          const m = out.slice(i).match(new RegExp(SENTINEL + '(\\d+)'));
          finish({ code: m ? parseInt(m[1], 10) : 0, out: out.slice(0, i), err: '' });
        }
      };
      const timer = setTimeout(() => finish({ code: -1, out: out.replace(/\r/g, ''), err: 'exec timeout' }), timeoutMs || 120000);
      try { callForm(mode, cmd + `; echo ${SENTINEL}$?`, cbName); }
      catch (err) { finish({ code: -1, out: '', err: String(err) }); }
    });
  }

  // 形态探测：结果持久缓存到 localStorage——此前每次打开页面都重新探测，且
  // 串行各等 4s（降级形态的管理器上光探测就吃 8 秒），曾是面板慢的主因之一。
  const MODE_CACHE_KEY = '__kmod_exec_mode';
  function readModeCache() {
    try {
      const m = localStorage.getItem(MODE_CACHE_KEY);
      return (m === 'cb3' || m === 'cb2' || m === 'promise') ? m : null;
    } catch { return null; }
  }
  function writeModeCache(m) { try { localStorage.setItem(MODE_CACHE_KEY, m); } catch {} }
  function clearModeCache() { try { localStorage.removeItem(MODE_CACHE_KEY); } catch {} }

  // 形态探测：echo 带形态标记，2.5 秒内收回即认定该形态可用
  function probe(mode, tag) {
    let out = '', settled = false;
    const cbName = '_ksu_probe_' + tag;
    const done = new Promise(resolve => {
      window[cbName] = function (chunk) {
        out = appendChunk(out, chunk);
        if (out.indexOf('KPROBE_' + tag.toUpperCase()) !== -1 && !settled) {
          settled = true; resolve(true);
        }
      };
      setTimeout(() => { if (!settled) { settled = true; resolve(false); } }, 2500);
    });
    try { callForm(mode, `echo KPROBE_${tag.toUpperCase()}`, cbName); }
    catch (e) { return Promise.resolve(false); }
    return done;
  }

  async function detectMode() {
    if (_mode) return _mode;
    const saved = readModeCache();
    if (saved) { _mode = saved; return _mode; }
    // 并行探测（各 2.5s 上限），全部失败降级 promise
    const [cb3, cb2] = await Promise.all([probe('cb3', 'cb3'), probe('cb2', 'cb2')]);
    _mode = cb3 ? 'cb3' : cb2 ? 'cb2' : 'promise';
    writeModeCache(_mode);
    return _mode;
  }

  function execForm(mode, cmd, timeoutMs) {
    if (mode === 'cb3' || mode === 'cb2') {
      const cbName = '_ksu_cb_' + Math.random().toString(36).slice(2);
      return sentinelExec(mode, cmd, cbName, timeoutMs);
    }
    // promise 降级形态：整体 base64 包裹（见 _cmds.promiseWrap），
    // 收回后解码——多行输出（SQL 结果、dnsfwd 探测等）不再失真只剩末行
    const r = callForm('promise', _cmds.promiseWrap(cmd));
    if (r && typeof r.then === 'function') {
      return r.then(v => {
        const raw = String(typeof v === 'string' ? v : (v && v.stdout) || '').replace(/\r/g, '').trim();
        return { code: 0, out: b64Decode(raw), err: '' };
      }, () => ({ code: -1, out: '', err: 'exec rejected' }));
    }
    if (typeof r === 'string') return Promise.resolve({ code: 0, out: b64Decode(r.replace(/\r/g, '').trim()), err: '' });
    return Promise.resolve({ code: -1, out: '', err: 'ksu.exec 返回异常' });
  }

  async function rawExec(cmd, timeoutMs) {
    const tmo = timeoutMs || 120000;
    const mode = await detectMode();
    let r = await execForm(mode, cmd, tmo);
    if (r.err === 'exec timeout' && readModeCache()) {
      // 缓存的形态失联（如管理器升级改变了回调形态）：清缓存重新探测后重试一次
      clearModeCache();
      _mode = null;
      r = await execForm(await detectMode(), cmd, tmo);
    }
    return r;
  }

  // 串行执行：所有调用排队，杜绝并发串扰
  function sh(cmd, timeoutMs) {
    const run = () => rawExec(cmd, timeoutMs);
    const p = _chain.then(run, run);
    _chain = p.then(() => {}, () => {});
    return p;
  }

  // SQL 经 $DATA_DIR/cc.sql 临时文件执行（Android 无 /tmp）。
  // -list 强制管道输出；对 database is locked 等错误自动重试（引擎与 WebUI 共库）。
  function sqlFile(sql, timeoutMs) {
    const f = cfg().DATA_DIR + '/cc.sql';
    const moddir = cfg().MODDIR;
    const run = () => sh(`cat > ${f} <<'__EOSQL__'\n${sql}\n__EOSQL__\n${moddir}/bin/sqlite3 -list ${cfg().DATA_DIR}/db/data.sqlite < ${f}`, timeoutMs);
    const attempt = n => run().then(r => {
      if (r.err && /locked|SQL error|unable/i.test(r.err) && n < 3) {
        return new Promise(res => setTimeout(res, 1000)).then(() => attempt(n + 1));
      }
      return r;
    });
    return attempt(1);
  }

  function ops(subcmd, timeoutMs) {
    return sh(`${cfg().MODDIR}/lib/ops.sh ${subcmd}`, timeoutMs);
  }

  // ═══════════ 命名操作层 ═══════════
  // 构造器（纯函数，导出 _cmds 供离线测试）+ 薄包装（真正执行）。
  // 原则：路径/URL/内容一律过 shq；文件内容走 base64 通道（任意字符安全，
  // 包括 heredoc 定界符、单引号、换行——曾因内联 heredoc/strip 引号踩坑）。

  const _cmds = {
    readFile: p => `cat ${shq(p)} 2>/dev/null`,
    // 内容 base64 通道 + 临时文件原子落盘
    writeFile: (p, content) =>
      `printf '%s' ${shq(b64Encode(content))} | base64 -d > ${shq(p)}.tmp && mv ${shq(p)}.tmp ${shq(p)}`,
    // 追加单行（换行折叠为空格，防止一行变多行）
    appendLine: (p, line) =>
      `printf '%s\\n' ${shq(String(line == null ? '' : line).replace(/\n/g, ' '))} >> ${shq(p)}`,
    remove: p => `rm -f ${shq(p)}`,
    // 备份一次（dst 存在即跳过）——"恢复初始默认"的回滚点
    // 备份一次（dst 已存在则不覆盖）→ 回滚点是否可用**由输出回答**（不再是"永远 true"）：
    //   ok=刚备份成功 / exists=已有备份 / no-src=没有原配置（没有可丢的东西）→ 都算可用
    //   fail=cp 失败 → 调用方据此拒绝改写："备份失败还继续写"会把用户配置置于无回滚点的状态
    backupOnce: (src, dst) =>
      `if [ ! -f ${shq(src)} ]; then echo no-src; elif [ -f ${shq(dst)} ]; then echo exists; ` +
      `elif cp ${shq(src)} ${shq(dst)} 2>/dev/null; then echo ok; else echo fail; fi`,
    // 从备份恢复 → true/false
    restoreBackup: (src, dst) =>
      `[ -f ${shq(src)} ] && cp ${shq(src)} ${shq(dst)} && echo ok || echo none`,
    probeDns: configFile =>
      `${cfg().MODDIR}/bin/dnsfwd -f ${shq(configFile)} -P -j 8 2>&1`,
    curlTiming: url =>
      `curl -o /dev/null -s -m 8 -w '%{http_code} %{time_total}' ${shq(url)}`,
    fetch: (url, sec) => `curl -s -m ${sec || 15} ${shq(url)}`,
    // -f（--fail）：HTTP ≥400 直接非零退出，不再"404 也写出正文并报成功"。
    // 2026-09-26 事故：加速节点返回 404 正文 "Not Found"（9 字节）被当引擎装上。
    download: (url, out, sec) =>
      `curl -fsSL -m ${sec || 300} -o ${shq(out)} ${shq(url)} && echo dl-ok`,
    sha256: p => `sha256sum ${shq(p)}`,
    // 装前门禁的两个只读探针（engineFileGate 的输入）：体积 + 前 4 字节十六进制
    fileSize: p => `wc -c < ${shq(p)} 2>/dev/null | tr -d '[:space:]'`,
    elfMagic: p => `head -c 4 ${shq(p)} 2>/dev/null | od -An -tx1 | tr -d '[:space:]'`,
    zipList: p => `unzip -l ${shq(p)}`,
    // SQL 结果以 INSERT 语句形式落盘（误删回滚快照）。
    // 必须**自己判成败**：sqlite3 出错时 `>` 仍会创建 0 字节文件，而 exec 层拿不到 stderr、
    // 退出码也不被 sentinelExec 上报（只有 code 字段）。真机实证：坏 SQL → `rc=1 size=0`，
    // 而现场 `backups/kv-before-orphan-clean-*.sql` 就是 0 字节 —— 界面却说"快照已存"，
    // 于是"误删可回滚"是假的（数据安全级）。故：退出码 + 文件非空双判据，失败即删空文件。
    sqlSnapshot: (sql, out) => {
      const c = cfg();
      const dir = out.slice(0, out.lastIndexOf('/'));
      const dump = `${shq(c.MODDIR + '/bin/sqlite3')} ${shq(c.DATA_DIR + '/db/data.sqlite')} ".mode insert kv" ${shq(sql)}`;
      return `mkdir -p ${shq(dir)}; `
        + `{ ${dump} > ${shq(out)} 2>/dev/null && [ -s ${shq(out)} ] && echo snap-ok; } || { rm -f ${shq(out)}; echo snap-fail; }`;
    },
    // promise 降级形态包裹：多行输出整体 base64 收敛为单行，"只剩末行"的实现
    // 也能收回完整输出（Phase 4：该环境补偿从此只存在于 bridge 一层）
    promiseWrap: cmd => `{ ${cmd}\n} 2>/dev/null | base64 | tr -d '\n'`
  };

  function readFile(path, tmo) { return sh(_cmds.readFile(path), tmo || 30000); }
  async function writeFile(path, content) {
    const r = await sh(_cmds.writeFile(path, content), 30000);
    return !r.err;
  }
  async function appendLine(path, line) {
    const r = await sh(_cmds.appendLine(path, line), 30000);
    return !r.err;
  }
  async function remove(path) { await sh(_cmds.remove(path)); return true; }
  // 诚实返回：只有 shell 明确回了 ok / exists / no-src 才算"回滚点可用"
  async function backupOnce(src, dst) {
    const r = await sh(_cmds.backupOnce(src, dst), 30000);
    const v = r.out.trim();
    return v === 'ok' || v === 'exists' || v === 'no-src';
  }
  async function restoreBackup(src, dst) {
    const r = await sh(_cmds.restoreBackup(src, dst), 30000);
    return r.out.trim() === 'ok';
  }
  function probeDns(configFile, tmo) { return sh(_cmds.probeDns(configFile), tmo || 60000); }
  async function curlTiming(url) {
    const r = await sh(_cmds.curlTiming(url), 15000);
    const m = r.out.trim().match(/^(\d{3}) ([0-9.]+)$/);
    return m && m[1] === '200' ? { ok: true, ms: parseFloat(m[2]) * 1000 } : { ok: false, ms: 0 };
  }
  function fetch(url, sec) { return sh(_cmds.fetch(url, sec), (sec || 15) * 1000 + 5000); }
  async function download(url, out, sec) {
    const r = await sh(_cmds.download(url, out, sec), (sec || 300) * 1000 + 5000);
    return r.out.includes('dl-ok');
  }
  async function sha256(path) {
    const r = await sh(_cmds.sha256(path), 60000);
    return r.out.trim().split(/\s/)[0] || '';
  }
  // 装前门禁探针：读不到就返回 0 / 空串，由纯函数 engineFileGate 判死（不在这里判）
  async function fileSize(path) {
    const r = await sh(_cmds.fileSize(path), 15000);
    return parseInt(r.out.trim(), 10) || 0;
  }
  async function elfMagic(path) {
    const r = await sh(_cmds.elfMagic(path), 15000);
    return r.out.trim().toLowerCase();
  }
  function zipList(path) { return sh(_cmds.zipList(path), 60000); }
  async function sqlSnapshot(sql, outFile) {
    // 只认命令自己吐出的 snap-ok：`!r.err` 会把"sqlite3 报错 + 0 字节文件"判成成功（见 _cmds.sqlSnapshot）
    const r = await sh(_cmds.sqlSnapshot(sql, outFile), 60000);
    return !r.err && r.out.includes('snap-ok');
  }

  return {
    sh, sqlFile, ops, detectMode,
    readFile, writeFile, appendLine, remove, backupOnce, restoreBackup,
    probeDns, curlTiming, fetch, download, sha256, zipList, sqlSnapshot,
    fileSize, elfMagic,
    b64Decode,
    _cmds // 纯函数构造器，供离线测试
  };
});
