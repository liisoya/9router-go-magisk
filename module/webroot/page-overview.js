// @ts-check
/* page-overview.js — 概览页：运行状态 / 服务地址 / 资源占用 / 引擎端口
 *
 * 首屏只走**一次** KB.ops('panel')（status + meminfo + RSS + upstreams + 两个配置）；
 * 原先这里是 9 次串行 root shell —— 每次 ksu.exec 都要新起 root shell 且全局串行排队，
 * 那曾是首屏慢的根源。刷新入口 refresh() 由 app-boot.js 调用。
 * 2026-09-28 从 app.js 纯搬迁（零行为变化）。
 */

const PORT_FILE = CFG.DATA_DIR + '/port';
// ── 首屏快照缓存：上次 panel 结果先渲染（秒出），panel 在后台刷新后覆盖 ──
const PANEL_CACHE_KEY = 'kmod-panel-snapshot';
function loadPanelCache() {
  try {
    const s = localStorage.getItem(PANEL_CACHE_KEY);
    const st = s ? JSON.parse(s) : null;
    return st && st.port ? st : null;
  } catch { return null; }
}
function savePanelCache(st) { try { localStorage.setItem(PANEL_CACHE_KEY, JSON.stringify(st)); } catch {} }

async function refresh() {
  const cached = loadPanelCache();
  if (cached) await applySnapshotToAll(cached);
  const t0 = Date.now();
  let st;
  try {
    st = KP.parseOpsStatus((await KB.ops('panel')).out);
    // 真机 2026-10-01：ops.sh 跑成功但只吐出空行时，parse 不抛错、拿到一个全空对象。
    // 那和"取数失败"在界面上完全一样（三块读数牌全「未知」、端口框显示 undefined），
    // 却没有任何提示 —— 把它当成失败，好让人看得到。
    if (!st.port) throw new Error('ops.sh panel 未返回端口（输出为空或解析不到 port）');
  } catch (e) {
    // 有缓存**也要说**：原先 `if (!cached)` 把失败吞掉了，于是"有旧快照"时取数失败永远静默，
    // 界面一直停在过期的空快照上 —— 这正是真机"点刷新拿不到数据"最难排查的形态。
    const msg = '状态获取异常：' + (e && e.message || e);
    if (!cached) $id('st-eng').textContent = msg;
    else toast('⚠️ ' + msg + '（当前显示的是上次缓存）', 5000);
    console.error('[9r-panel]', e);
    return cached;
  }
  console.log('[9r-panel] panel 耗时', (Date.now() - t0) + 'ms');
  if (st.port) savePanelCache(st);
  await applySnapshotToAll(st);
  return st;
}

/** 概览页的快照应用器（自注册，refresh 广播时调用）
 *  只碰概览自己的 DOM —— DNS 页与更新页的装载各归各页（2026-10-01 架构评审 #2）。 */
function applyOverview(st, live) {
  // 诊断探针：仅实时数据缺 port 时展示（快照渲染不动诊断区）
  if (live && !st.port) {
    const diag = document.getElementById('diag');
    diag.style.display = 'block';
    const idRes = KB.sh('id');
    const rawRes = KB.ops('status'); // 诊断也走 KB.ops，不内联拼 ops.sh 路径
    const lsRes = KB.diag.libListing();
    Promise.all([idRes, rawRes, lsRes]).then(([idR, rawR, lsR]) => {
      diag.textContent =
        '【诊断】ops.sh status 原始输出: ' + JSON.stringify(rawR).slice(0, 300) +
        '\n【诊断】id: ' + esc(idR.out.trim() || idR.err.trim()) +
        '\n【诊断】lib/ 目录: ' + esc(lsR.out.trim() || lsR.err.trim());
    });
  } else {
    document.getElementById('diag').style.display = 'none';
  }
  // 状态词 → 文案/严重度：唯一映射在 KP.stateLabel / KP.stateTile（页面不再手写 if/else 链；
  // 加一个意图态只改 parsers.js 一处，且门禁会把 shell emit 与这张表缝死）
  renderStatusTiles(st);
  document.getElementById('in-port').value = st.port;
  document.getElementById('st-ver').textContent = st.engine_version || '未知';
  document.getElementById('eng-cur').textContent = st.engine_version || '未知';
  // 版本来源自检（Phase 26）从"概览的一行"挪到「引擎版本」卡片的悬停提示：
  // 它是运维取证信息（真机门禁 T12 也断言这两个字段），但不该常驻占版面。
  const verTip = document.getElementById('ver-tip');
  if (verTip) verTip.title = KP.engineVersionSourceLabel(st.engine_ver_src, st.engine_ver_healed === '1');
  document.getElementById('mod-cur').textContent = st.module_version || '未知';
  // 跨页派生量的归一化在 applySnapshotToAll 里先做过（页面不再各自 trim / || DEFAULT）
  resources(st);
  renderAddrs(st);
  // 出厂 key 卡片已移除（用户无感）：新装由 service.sh 开机自动补入；
  // 导入场景仪表盘走会话鉴权不再依赖 apiKeys 表（引擎 Phase 10 修复）
}
onPanelSnapshot(applyOverview);

/** 概览三块读数：**底色**表状态（绿=正常 / 灰=退出 / 琥珀=留意 / 红=故障），数值只留 PID。
 *
 * 为什么不把颜色写在数字上：三块并排时，三个不同颜色的数字比三块底色更难一眼扫完
 * （2026-09-30 用户反馈「显示运行的状态感觉很多、很乱」）。
 * 短形态由 KP.stateTile 出（完整句仍归 DNS 页的 kv 行）。
 */
function renderStatusTiles(st) {
  const tiles = [
    { box: 'ro-eng', val: 'st-eng', kind: 'engine',   value: st.engine,   pid: st.engine_pid },
    { box: 'ro-dns', val: 'st-dns', kind: 'dns',      value: st.dns,      pid: st.dns_pid },
    { box: 'ro-wd',  val: 'st-wd',  kind: 'watchdog', value: st.watchdog, pid: st.watchdog_pid },
  ];
  for (const t of tiles) {
    const tile = KP.stateTile(t.kind, t.value, t.pid);
    const box = document.getElementById(t.box);
    if (box) box.className = 'ro ' + tile.tone;
    const val = document.getElementById(t.val);
    if (val) val.textContent = tile.text;
  }
}

// ── 服务地址卡：本机 + 局域网 Dashboard 地址（panel 的 lan_ip token）──
async function copyText(s) {
  try { await navigator.clipboard.writeText(s); toast('已复制：' + s); }
  catch {
    // webview 剪贴板 API 不可用时的兜底
    const ta = document.createElement('textarea');
    ta.value = s; document.body.appendChild(ta); ta.select();
    try { document.execCommand('copy'); toast('已复制：' + s); }
    catch { toast('复制失败，请长按地址手动复制', 3600); }
    ta.remove();
  }
}
function renderAddrs(st) {
  const box = document.getElementById('addr-list');
  if (!box) return;
  if (!st.port) { box.innerHTML = '<div class="hint">（引擎未运行，无服务地址）</div>'; return; }
  const port = st.port;
  const rows = [{ label: '本机', url: `http://127.0.0.1:${port}` }];
  (st.lan_ip || '').split('|').map(s => s.trim()).filter(Boolean).forEach(ip =>
    rows.push({ label: '局域网', url: `http://${ip}:${port}` }));
  // 「本机 / 局域网」是同级的两个地址来源，标牌不给其中一个上主题色 ——
  // 一个灰一个橙会读成"局域网更特别"，而它们只是并列的两行（2026-09-30 用户反馈）
  box.innerHTML = rows.map(r =>
    `<div class="list-item"><span class="tag">${esc(r.label)}</span>` +
    `<a class="addr" href="${escAttr(r.url)}" target="_blank" rel="noopener">${esc(r.url)}</a>` +
    `<button data-u="${escAttr(r.url)}">复制</button></div>`).join('');
  box.querySelectorAll('button[data-u]').forEach(b => { b.onclick = () => copyText(b.dataset.u); });
}
function resources(st) {
  // 2026-09-30：独立的「资源占用」卡片取消，内存直接并进运行状态的两块读数牌；
  // 系统可用/总量不再显示。**阈值告警保留**（超阈值时把数字染色）——
  // 那是"引擎吃内存"的唯一可见信号，卡片可以少，信号不能丢。
  const fmt = KP.fmtMem;   // 格式化是纯函数，唯一所有者在 parsers.js
  const put = (id, kb, warnKb, errKb) => {
    const el = document.getElementById(id);
    if (!el) return;
    el.textContent = fmt(kb);
    el.style.color = (errKb && kb > errKb) ? 'var(--err)'
      : (warnKb && kb > warnKb) ? 'var(--warn)' : '';
  };
  put('mem-eng', parseInt(st.engine_rss, 10) || 0, 204800, 307200);  // 200MB 黄 / 300MB 红
  // 只写概览自己的两块牌；DNS 页那块由 DNS 页自己写（2026-10-01 架构评审 #2）
  put('mem-dns', parseInt(st.dns_rss, 10) || 0);
}
// ── 生命周期动作互斥锁 ──
// withBusy 只 disable 掉**被点的那个按钮**，而启动/停止/重启/改端口操作的是同一批进程 ——
// 用户在"启动中"改点"停止"，两个 shell 动作就并发打在同一批 pid 上（一个在拉、一个在杀，
// 界面状态跟着乱跳）。这里再加一层动作级互斥：进行中再点，明确回一句"正在…"，而不是报错。
let _lifecycleBusy = '';
async function runLifecycle(o) {
  if (_lifecycleBusy) { toast(`⏳ ${_lifecycleBusy}，请稍候（已忽略本次点击）`, 2600); return false; }
  _lifecycleBusy = o.busyText || '操作进行中';
  try { return await runOpsAction(o); } finally { _lifecycleBusy = ''; }
}
async function restartAll() {
  // 引擎与 DNS 是**同级别**的两项，且 DNS 由守护异步拉起、比引擎晚（真机 ≈21s）：
  // ops.sh 会等到两者都到真实终态才返回（见 life_wait_dns_settled），返回即刷新，
  // 所以这里看到的就是两者**同一时刻**的事实，不会出现"引擎绿 / DNS 红"。
  toast('重启中…（引擎 + DNS 都到位才返回，约 20-30 秒）');
  // 生命周期唯一入口：ops.sh restart-engine（内置等待，调用即知结果）。
  // 诚实回执走 runOpsAction（唯一实现，见 app-core.js）—— 原先这四个动作各写一遍
  // "比对状态词 → 挑 toast"，A5/A5b 两次谎报成功都出在这种重复里。
  return runLifecycle({
    busyText: '重启中',
    btn: $id('btn-restart-all'), label: '重启中…', subcmd: 'restart-engine',
    okMsg: '✅ 引擎与 DNS 已重启',
    failMsg: (r) => String((r && r.out) || '').trim() === 'dns-pending'
      // 引擎起来了、DNS 没到终态 ≠ "引擎未拉起"：混成一句会让人去查错的地方
      ? '⚠️ 引擎已重启，但 DNS 未在 30 秒内起来（详见 DNS 日志）'
      : '❌ 引擎未拉起，请查看引擎日志'
  });
}
async function savePort() {
  return withBusy($id('btn-port'), '写入中…', async () => {
    const p = $id('in-port').value.trim();
    if (!/^[0-9]+$/.test(p) || +p < 1 || +p > 65535) { toast('端口必须是 1-65535 的数字'); return; }
    if (!await KB.writeFile(PORT_FILE, p + '\n')) { toast('❌ 端口写入失败，未改动'); return; }
    toast('端口已写入 ' + p + '，重启引擎…');
    // 必须判 restart-engine 的结果再报成功（2026-09-29 架构走查 A5）：
    // 过去这里丢弃返回值并无条件 toast「✅ 引擎已在端口 p 重启」—— 新端口被占用/引擎起不来时
    // 界面照样报成功（把失败当成功，本仓库最忌讳的一类）；同文件的 restartAll 一直是在判的，
    // 两条重启路径不该有不同的诚实度。这里不再包 btn（外层 withBusy 已经占着 btn-port）。
    await runLifecycle({
      busyText: '重启中', subcmd: 'restart-engine',
      okMsg: '✅ 引擎已在端口 ' + p + ' 重启',
      failMsg: '❌ 端口已写入 ' + p + '，但引擎未拉起（详见引擎日志）'
    });
  });
}
async function startSvc() {
  // 用户显式启服务 = **全部启动**（守护 / 引擎 / DNS，唯一入口 ops.sh start-user）。
  // 成功词集（engine=up / engine=running）由 KP.ACTION_WORDS 管：幂等分支回 running
  // 也算成功 —— 只认 engine=up 会把"好端端跑着"判成失败（2026-10-01），词表就是那起
  // 事故面的收口，这里不再手抄。
  return runLifecycle({
    busyText: '启动中',
    btn: $id('btn-start-svc'), label: '启动中…', subcmd: 'start-user',
    okMsg: (r) => KP.isAlreadyRunning(r && r.out)
      ? '✅ 服务已在运行（引擎 / DNS 均正常，无需重复启动）'
      : '✅ 服务已启动（引擎 / DNS / 守护）',
    failMsg: (r) => String((r && r.out) || '').trim() === 'dns-pending'
      ? '⚠️ 引擎已启动，但 DNS 未在 30 秒内起来（详见 DNS 日志）'
      : '❌ 服务未启动（详见引擎日志）'
  });
}
async function stopSvc() {
  // 用户显式停服务 = **全停**（引擎 + DNS + 守护，见 life_stop_user）：
  // 过去守护不参与停止，界面 watchdog 仍是 up，与"我已经停了"的直觉打架。
  return runLifecycle({
    busyText: '停止中',
    btn: $id('btn-stop-svc'), label: '停止中…', subcmd: 'stop-user',
    okMsg: '已全部停止（引擎 / DNS / 守护；点「启动服务」全部恢复）', okMs: 5200,
    failMsg: '❌ 停止失败（详见引擎日志）'
  });
}
$id('btn-refresh').onclick = refresh;
$id('btn-restart-all').onclick = restartAll;
$id('btn-start-svc').onclick = startSvc;
$id('btn-stop-svc').onclick = stopSvc;
$id('btn-port').onclick = savePort;
