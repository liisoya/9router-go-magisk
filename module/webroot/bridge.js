/* bridge.js — root-shell 资源访问层（KSU ksu.exec 桥）
 *
 * 深模块：环境差异全部补偿在这里 ——
 *   - 全局串行队列（并发 ksu.exec 在部分 KernelSU 构建上输出交叉/丢失）
 *   - CRLF 剥离（ksu.exec 返回 \r\n）
 *   - SQL 一律经 $CFG.DATA_DIR/cc.sql 临时文件执行（Android 无 /tmp；
 *     sqlite3 -list 强制管道输出，该构建默认 box 表格模式会污染解析）
 *
 * 依赖：index.html 先注入 window.CFG = { MODDIR, DATA_DIR }，再加载本文件。
 */
(function (root, factory) {
  if (typeof module === 'object' && module.exports) module.exports = factory();
  else root.KBridge = factory();
})(typeof self !== 'undefined' ? self : this, function () {
  'use strict';

  let _chain = Promise.resolve();

  function rawExec(cmd) {
    if (typeof ksu === 'undefined' || !ksu.exec) {
      return Promise.resolve({ code: -1, out: '', err: 'WebUI 桥不可用' });
    }
    // 兼容两种形态：Promise 返回 / 全局 callback 回传
    try {
      const r = ksu.exec(cmd);
      if (r && typeof r.then === 'function') {
        return r.then(normalize, () => ({ code: -1, out: '', err: 'exec rejected' }));
      }
      if (typeof r === 'string') return Promise.resolve({ code: 0, out: r, err: '' });
      return Promise.resolve(normalizeObj(r));
    } catch (e) { /* fallthrough 到 callback 形态 */ }
    return new Promise(resolve => {
      const cb = '_ksu_cb_' + Math.random().toString(36).slice(2);
      window[cb] = res => {
        delete window[cb];
        if (typeof res === 'string') return resolve({ code: 0, out: res, err: '' });
        resolve(normalizeObj(res));
      };
      try { ksu.exec(cmd, '{}', cb); }
      catch (err) { resolve({ code: -1, out: '', err: String(err) }); }
    });
  }
  function normalizeObj(r) {
    return {
      code: (r && r.errno) != null ? r.errno : ((r && r.code) || 0),
      out: (r && r.stdout) || '',
      err: (r && r.stderr) || ''
    };
  }
  function normalize(r) {
    return { code: 0, out: String(r == null ? '' : r), err: '' };
  }

  // 串行执行：所有调用排队，杜绝并发串扰。
  // 空响应（out 与 err 均空且 code=0）在 KSU 上偶发（调用被丢弃），自动重试一次。
  function sh(cmd) {
    const run = () => rawExec(cmd).then(r => ({
      code: r.code, out: String(r.out || '').replace(/\r/g, ''), err: String(r.err || '')
    }));
    const attempt = () => run().then(r => {
      if ((r.out === '' && r.err === '' && r.code === 0) && !/sleep|rm -f/.test(cmd)) {
        return run(); // 疑似丢包，重试一次（对写文件类命令也安全：多为幂等读）
      }
      return r;
    });
    const p = _chain.then(attempt, attempt);
    _chain = p.then(() => {}, () => {});
    return p;
  }

  // SQL 经 $DATA_DIR/cc.sql 临时文件执行（Android 无 /tmp）
  // -list 强制管道分隔输出（该 sqlite3 构建默认 box 表格模式会污染解析）。
  // 引擎持有同库（WAL）：读撞上引擎写事务会报 database is locked / 输出半截，
  // 这里对任何 sqlite3 报错自动重试（最多 3 次、间隔 1s）。
  function sqlFile(sql) {
    const f = (window.CFG && window.CFG.DATA_DIR || '/data/adb/9router-go') + '/cc.sql';
    const moddir = window.CFG && window.CFG.MODDIR;
    const run = () => sh(`cat > ${f} <<'__EOSQL__'\n${sql}\n__EOSQL__\n${moddir}/bin/sqlite3 -list ${window.CFG.DATA_DIR}/db/data.sqlite < ${f}; rm -f ${f}`);
    const attempt = n => run().then(r => {
      if (r.err && /locked|SQL error|unable/i.test(r.err) && n < 3) {
        return new Promise(res => setTimeout(res, 1000)).then(() => attempt(n + 1));
      }
      return r;
    });
    return attempt(1);
  }

  function ops(subcmd) {
    const moddir = window.CFG && window.CFG.MODDIR;
    return sh(`${moddir}/lib/ops.sh ${subcmd}`);
  }

  return { sh, sqlFile, ops };
});
