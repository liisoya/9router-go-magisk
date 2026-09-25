/* bridge.js — root-shell 资源访问层（KSU ksu.exec 桥）
 *
 * 深模块：环境差异全部补偿在这里 ——
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

  // 按形态发起调用（不管理完成判定）
  function callForm(mode, cmd, cbName) {
    if (mode === 'cb3') return ksu.exec(cmd, '{}', cbName);
    if (mode === 'cb2') return ksu.exec(cmd, cbName);
    if (mode === 'promise') return ksu.exec(cmd);
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
        if (typeof chunk === 'string') out += chunk;
        else if (chunk && typeof chunk.stdout === 'string') out += chunk.stdout;
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

  // 形态探测：echo 带形态标记，4 秒内收回即认定该形态可用
  async function detectMode() {
    if (_mode) return _mode;
    const probe = async (mode, tag) => {
      let out = '', settled = false;
      const cbName = '_ksu_probe_' + tag;
      const done = new Promise(resolve => {
        window[cbName] = function (chunk) {
          if (typeof chunk === 'string') out += chunk;
          else if (chunk && typeof chunk.stdout === 'string') out += chunk.stdout;
          if (out.indexOf('KPROBE_' + tag.toUpperCase()) !== -1 && !settled) {
            settled = true; resolve(true);
          }
        };
        setTimeout(() => { if (!settled) { settled = true; resolve(false); } }, 4000);
      });
      try { callForm(mode, `echo KPROBE_${tag.toUpperCase()}`, cbName); }
      catch (e) { return false; }
      return done;
    };
    if (await probe('cb3', 'cb3')) { _mode = 'cb3'; return _mode; }
    if (await probe('cb2', 'cb2')) { _mode = 'cb2'; return _mode; }
    _mode = 'promise'; // 降级：多行输出只剩末行，上层应优先使用单行命令
    return _mode;
  }

  function rawExec(cmd, timeoutMs) {
    const tmo = timeoutMs || 120000;
    return detectMode().then(mode => {
      if (mode === 'cb3' || mode === 'cb2') {
        const cbName = '_ksu_cb_' + Math.random().toString(36).slice(2);
        return sentinelExec(mode, cmd, cbName, tmo);
      }
      // promise 降级形态：输出可能只剩末行
      const r = callForm('promise', cmd);
      if (r && typeof r.then === 'function') {
        return r.then(v => ({
          code: 0,
          out: String(typeof v === 'string' ? v : (v && v.stdout) || '').replace(/\r/g, ''),
          err: ''
        }), () => ({ code: -1, out: '', err: 'exec rejected' }));
      }
      if (typeof r === 'string') return { code: 0, out: r.replace(/\r/g, ''), err: '' };
      return { code: -1, out: '', err: 'ksu.exec 返回异常' };
    });
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
    const f = (window.CFG && window.CFG.DATA_DIR || '/data/adb/9router-go') + '/cc.sql';
    const moddir = window.CFG && window.CFG.MODDIR;
    const run = () => sh(`cat > ${f} <<'__EOSQL__'\n${sql}\n__EOSQL__\n${moddir}/bin/sqlite3 -list ${window.CFG.DATA_DIR}/db/data.sqlite < ${f}`, timeoutMs);
    const attempt = n => run().then(r => {
      if (r.err && /locked|SQL error|unable/i.test(r.err) && n < 3) {
        return new Promise(res => setTimeout(res, 1000)).then(() => attempt(n + 1));
      }
      return r;
    });
    return attempt(1);
  }

  function ops(subcmd, timeoutMs) {
    const moddir = window.CFG && window.CFG.MODDIR;
    return sh(`${moddir}/lib/ops.sh ${subcmd}`, timeoutMs);
  }

  return { sh, sqlFile, ops, detectMode };
});
