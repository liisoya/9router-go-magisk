/* bridge.js — root-shell 资源访问层（KSU ksu.exec 桥）
 *
 * 深模块：环境差异全部补偿在这里 ——
 *   - 回调累积 + 完成哨兵：部分 KernelSU 管理器的 ksu.exec 按块回调输出，
 *     Promise 形式只保留最后一个块（曾导致多行输出只剩末行：版本"未知"、
 *     资源"-"、孤儿扫描只看见最后一个 key）。累积全部块，以
 *     `echo __KMOD_DONE__$?` 哨兵判定结束并携带退出码。
 *   - 全局串行队列（并发 ksu.exec 在部分构建上输出交叉/丢失）
 *   - CRLF 剥离
 *   - SQL 经 $DATA_DIR/cc.sql 临时文件执行（Android 无 /tmp；-list 强制
 *     管道输出，该 sqlite3 构建默认 box 表格模式会污染解析）；对
 *     database is locked 等错误自动重试（引擎与 WebUI 共库）
 *
 * 依赖：index.html 先注入 window.CFG = { MODDIR, DATA_DIR }，再加载本文件。
 */
(function (root, factory) {
  if (typeof module === 'object' && module.exports) module.exports = factory();
  else root.KBridge = factory();
})(typeof self !== 'undefined' ? self : this, function () {
  'use strict';

  let _chain = Promise.resolve();

  const SENTINEL = '__KMOD_DONE__';

  // 回调累积式执行：兼容"流式多块回调"与"一次性完整回调"两种管理器形态
  function rawExec(cmd, timeoutMs) {
    const tmo = timeoutMs || 120000;
    return new Promise(resolve => {
      if (typeof ksu === 'undefined' || !ksu.exec) {
        return resolve({ code: -1, out: '', err: 'WebUI 桥不可用' });
      }
      let out = '', settled = false;
      const cbName = '_ksu_cb_' + Math.random().toString(36).slice(2);
      const finish = r => {
        if (settled) return;
        settled = true;
        delete window[cbName];
        clearTimeout(timer);
        resolve({ code: r.code, out: String(r.out || '').replace(/\r/g, ''), err: String(r.err || '') });
      };
      window[cbName] = function (chunk) {
        if (settled) return;
        if (typeof chunk === 'string') {
          out += chunk;                                   // 流式：stdout 文本块
        } else if (chunk && typeof chunk.stdout === 'string') {
          out += chunk.stdout;                            // 整体：{stdout,...}
        } else if (chunk && typeof chunk === 'object') {
          // 未知对象形态：尽量提取可用字段后按完成处理
          if (chunk.errno != null && !out) {
            finish({ code: chunk.errno, out: '', err: chunk.stderr || '' });
          }
          return;
        }
        const i = out.indexOf(SENTINEL);
        if (i !== -1) {
          const tail = out.slice(i);
          const m = tail.match(new RegExp(SENTINEL + '(\\d+)'));
          finish({ code: m ? parseInt(m[1], 10) : 0, out: out.slice(0, i), err: '' });
        }
      };
      // 兜底超时：哨兵迟迟不到（进程异常/管理器吞回调）时按已有输出结算，避免队列永久卡死
      const timer = setTimeout(() => finish({ code: -1, out: out.replace(/\r/g, ''), err: 'exec timeout' }), tmo);
      try {
        // 哨兵携带退出码：`echo __KMOD_DONE__$?` 永远是最后一条输出
        ksu.exec(cmd + `; echo ${SENTINEL}$?`, '{}', cbName);
      } catch (err) {
        finish({ code: -1, out: '', err: String(err) });
      }
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
    const run = () => sh(`cat > ${f} <<'__EOSQL__'\n${sql}\n__EOSQL__\n${moddir}/bin/sqlite3 -list ${window.CFG.DATA_DIR}/db/data.sqlite < ${f}; rm -f ${f}`, timeoutMs);
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

  return { sh, sqlFile, ops };
});
