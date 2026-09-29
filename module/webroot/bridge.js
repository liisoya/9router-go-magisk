// @ts-check
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
  // 类型闸说明：`this` 分支转 any —— 本文件带了 module.exports，TS 按 CJS 模块处理，
  // 顶层 `this` 的类型是"模块导出对象"，而浏览器分支要的是宿主全局（同 parsers.js）。
})(typeof self !== 'undefined' ? self : /** @type {any} */ (this), function () {
  'use strict';

  let _chain = Promise.resolve();
  let _mode = null; // 探测到的可用形态：cb3 / cb2 / promise

  const SENTINEL = '__KMOD_DONE__';

  /** @returns {{ MODDIR?: string, DATA_DIR?: string }} */
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
      // 宿主按**名字**在全局里找回调（cb3/cb2 形态传的是回调名，不是函数）——
      // 注册与注销必须走一个宽松视图；这是动态名字边界，静态断言不了。
      const g = /** @type {Record<string, any>} */ (window);
      const finish = r => {
        if (settled) return;
        settled = true;
        delete g[cbName];
        clearTimeout(timer);
        resolve(r);
      };
      g[cbName] = function (chunk) {
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
  // 自报成败：sqlite3 失败（locked/语法错）时输出为空且 promise 形态下 stderr 被丢弃、
  // 退出码不可见 —— 必须由命令自己吐 __SQL_OK__，调用方凭 r.ok 区分"空结果"与"读失败"。
  function sqlFile(sql, timeoutMs) {
    const f = cfg().DATA_DIR + '/cc.sql';
    const moddir = cfg().MODDIR;
    const run = () => sh(`cat > ${f} <<'__EOSQL__'\n${sql}\n__EOSQL__\n${moddir}/bin/sqlite3 -list ${cfg().DATA_DIR}/db/data.sqlite < ${f} && echo __SQL_OK__`, timeoutMs);
    const attempt = n => run().then(r => {
      const ok = r.out.includes('__SQL_OK__');
      const failed = (r.err && /locked|SQL error|unable/i.test(r.err)) || !ok;
      if (failed && n < 3) {
        return new Promise(res => setTimeout(res, 1000)).then(() => attempt(n + 1));
      }
      // 标记由桥自己剥掉：调用方只见干净的 SQL 输出（标记行无 '|'，
      // 会被孤儿扫描误算进"存活节点"——桩测试抓出来的，别让调用方背环境噪音）
      return { ...r, ok, out: r.out.split('__SQL_OK__').join('') };
    });
    return attempt(1);
  }

  function ops(subcmd, timeoutMs) {
    return sh(_cmds.ops(subcmd), timeoutMs);
  }

  // ═══════════ 命名操作层 ═══════════
  // 构造器（纯函数，导出 _cmds 供离线测试）+ 薄包装（真正执行）。
  // 原则：路径/URL/内容一律过 shq；文件内容走 base64 通道（任意字符安全，
  // 包括 heredoc 定界符、单引号、换行——曾因内联 heredoc/strip 引号踩坑）。

  const _cmds = {
    readFile: p => `cat ${shq(p)} 2>/dev/null && echo __READ_OK__`,
    // 内容 base64 通道 + 临时文件原子落盘。自报成败（write-ok）：promise 形态下
    // 失败对 exec 层不可见（stderr 丢弃、退出码恒 0），不吐标记 writeFile 会永远报 true
    writeFile: (p, content) =>
      `printf '%s' ${shq(b64Encode(content))} | base64 -d > ${shq(p)}.tmp && mv ${shq(p)}.tmp ${shq(p)} && echo write-ok`,
    // 追加单行（换行折叠为空格，防止一行变多行），同 writeFile 自报成败
    appendLine: (p, line) =>
      `printf '%s\\n' ${shq(String(line == null ? '' : line).replace(/\n/g, ' '))} >> ${shq(p)} && echo append-ok`,
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
    // 批量测速（A5）：**并发放在 shell 内部** —— 单次 exec 里对每个节点起一个后台子 shell，
    // 各自把结果写进**自己的文件**，`wait` 后按索引统一 cat 出来。
    // 为什么不放开 exec 并发：bridge 有全局串行队列（并发 ksu.exec 在部分管理器上输出交叉/丢失），
    // 而 shell 内部并发不经过它 —— 也就不需要"按宿主差异化放开并发"那套机制。
    // 为什么每节点一个文件：curl 的 -w 与 printf 是分段写，共用 stdout 会互相插队；
    // 独立文件后输出零交错（cat 的 glob 顺序无所谓，索引写在行里，见 parsers.parseCurlTimings）。
    curlTimingBatch: (urls, tag) => {
      const dir = cfg().DATA_DIR + '/accel-batch';
      // `--connect-timeout 3`：失败节点是耗时大头（真机实测：挂死的代理 5.03s → 3.00s，
      // 因为 -m 8 会一直等；连不上就该 3s 内认输）。`%{errormsg}` 带出失败原因供界面显示
      // （curl 8.0.1 支持；不支持的旧 curl 只会少一段，解析侧按可选处理）。
      const jobs = (urls || []).map((u, i) =>
        `( printf '%s\\t%s\\t' ${shq(String(i))} ${shq(u)}; ` +
        `curl -o /dev/null -s -m 8 --connect-timeout 3 ` +
        `-w '%{http_code} %{time_total} %{errormsg}\\n' ${shq(u)} ) ` +
        `> ${shq(dir + '/' + tag + '-' + i + '.out')} 2>/dev/null &`).join(' ');
      return `mkdir -p ${shq(dir)}; ${jobs} wait; cat ${shq(dir)}/${shq(tag)}-*.out 2>/dev/null; rm -rf ${shq(dir)}`;
    },
    // -L（--location）不能省：GitHub release 资产的地址是 302 跳到 objects.githubusercontent.com，
    // 不跟随重定向时 curl 拿到的是 302 的空正文。真机实测（2026-09-27，直连 GitHub）：
    //   SHA256SUMS 无 -L → 302 size=0   ／ 有 -L → 200 size=453
    // 少了它，面板永远"取不到校验和"→ 按 fail-closed 直接拒绝更新（tag 修好了也白修）。
    // 同时**不加 -f**：这里要的是"把正文拿回来"，404 正文留给调用方展示（诊断信息）。
    fetch: (url, sec) => `curl -sL -m ${sec || 15} ${shq(url)}`,
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
    promiseWrap: cmd => `{ ${cmd}\n} 2>/dev/null | base64 | tr -d '\n'`,
    // ops.sh 子命令：脚本路径与每个 token 都过 shq（ver/路径不再裸拼——shq 是唯一转义出口）
    ops: sub => `${shq(cfg().MODDIR + '/lib/ops.sh')} ${String(sub == null ? '' : sub).trim().split(/\s+/).map(shq).join(' ')}`,
    // ── dbOps：SQL 文本唯一所有者（离线可断言；别名一律来自 computeOrphans，已过 UUID 形状校验）──
    scanOrphansSql: () =>
      `SELECT id FROM providerNodes;\nSELECT DISTINCT provider FROM providerConnections;\n` +
      `SELECT key FROM kv WHERE scope='customModels';\nSELECT key FROM kv WHERE scope='disabledModels';`,
    recheckOrphansSql: list =>
      (list || []).map(a =>
        `SELECT '${a}' WHERE EXISTS (SELECT 1 FROM providerNodes WHERE id='${a}') OR EXISTS (SELECT 1 FROM providerConnections WHERE provider='${a}');`).join('\n'),
    orphanSnapshotSql: list => {
      const like = (list || []).map(a => `key LIKE '${a}|%'`).join(' OR ');
      const eq = (list || []).map(a => `key='${a}'`).join(' OR ');
      return `SELECT * FROM kv WHERE scope IN ('customModels','disabledModels') AND (${like} OR ${eq});`;
    },
    orphanDeleteSql: list => {
      const like = (list || []).map(a => `key LIKE '${a}|%'`).join(' OR ');
      const eq = (list || []).map(a => `key='${a}'`).join(' OR ');
      return `DELETE FROM kv WHERE scope='customModels' AND (${like}); DELETE FROM kv WHERE scope='disabledModels' AND (${eq});`;
    },
    credScanSql: () =>
      `SELECT id || '|' || provider || '|' || authType || '|' || COALESCE(json_extract(data,'$.apiKey'),'null') || '|' || COALESCE(json_extract(data,'$.accessToken'),'null') || '|' || COALESCE(json_extract(data,'$.refreshToken'),'null') FROM providerConnections WHERE isActive=1;`,
    // 模块 lib 目录清单（页面诊断用；经此收敛，app.js 不再裸拼 shell）
    libListing: () => `ls -la ${cfg().MODDIR}/lib/ 2>&1`
  };

  // readFile 自报成败（read-ok）：与 write 族同一纪律 —— promise 形态下读失败与
  // "文件为空"都是空输出、不可辨；调用方凭 r.ok 区分（标记由桥剥掉，不污染内容）
  async function readFile(path, tmo) {
    const r = await sh(_cmds.readFile(path), tmo || 30000);
    const ok = r.out.includes('__READ_OK__');
    // 标记行连同其换行一起剥净，调用方拿到的就是纯文件内容
    return { ...r, ok, out: r.out.replace(/\n?__READ_OK__(\n|$)/g, '') };
  }
  // 只认命令自吐的 write-ok / append-ok：promise 形态下 r.err 恒空（stderr 丢弃、
  // 退出码恒 0），`!r.err` 会把设备上的写失败判成成功 —— 面板谎报"已保存"
  async function writeFile(path, content) {
    const r = await sh(_cmds.writeFile(path, content), 30000);
    return !r.err && r.out.includes('write-ok');
  }
  async function appendLine(path, line) {
    const r = await sh(_cmds.appendLine(path, line), 30000);
    return !r.err && r.out.includes('append-ok');
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
  // 批量测速：只回**原始输出**，解析交给纯函数（parsers.parseCurlTimings）——
  // 执行归 bridge、解析归 parsers，各自单向依赖（原先单点 curlTiming 把解析内联在这里，
  // 于是"输出形态"这件事有两个家；批量后收成一处）
  function curlTimingBatch(urls, tag) {
    return sh(_cmds.curlTimingBatch(urls, tag), 60000);
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
    probeDns, curlTimingBatch, fetch, download, sha256, zipList, sqlSnapshot,
    fileSize, elfMagic,
    b64Decode,
    _cmds // 纯函数构造器，供离线测试
  };
});
