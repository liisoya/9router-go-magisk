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
  if (cached) renderPanel(cached, false);
  const t0 = Date.now();
  let st;
  try {
    st = KP.parseOpsStatus((await KB.ops('panel')).out);
  } catch (e) {
    if (!cached) $id('st-eng').textContent = '状态获取异常: ' + (e && e.message || e);
    return cached;
  }
  console.log('[9r-panel] panel 耗时', (Date.now() - t0) + 'ms');
  if (st.port) savePanelCache(st);
  renderPanel(st, true);
  return st;
}

function renderPanel(st, live) {
  // 诊断探针：仅实时数据缺 port 时展示（快照渲染不动诊断区）
  if (live && !st.port) {
    const diag = document.getElementById('diag');
    diag.style.display = 'block';
    const idRes = KB.sh('id');
    const rawRes = KB.ops('status'); // 诊断也走 KB.ops，不内联拼 ops.sh 路径
    const lsRes = KB.sh(KB._cmds.libListing());
    Promise.all([idRes, rawRes, lsRes]).then(([idR, rawR, lsR]) => {
      diag.textContent =
        '【诊断】ops.sh status 原始输出: ' + JSON.stringify(rawR).slice(0, 300) +
        '\n【诊断】id: ' + esc(idR.out.trim() || idR.err.trim()) +
        '\n【诊断】lib/ 目录: ' + esc(lsR.out.trim() || lsR.err.trim());
    });
  } else {
    document.getElementById('diag').style.display = 'none';
  }
  // 状态词 → 文案/严重度：唯一映射在 KP.stateLabel（app.js 不再手写 if/else 链；
  // 加一个意图态只改 parsers.js 一处，且门禁会把 shell emit 与这张表缝死）
  const dnsDot = document.getElementById('dot-dns');
  const dnsS = KP.stateLabel('dns', st.dns, st.dns_pid);
  dnsDot.className = 'dot ' + dnsS.tone;
  const dnsTxt = dnsS.text;
  document.getElementById('st-dns').textContent = dnsTxt;
  document.getElementById('dnsw-state').textContent = dnsTxt;
  // 守护：引擎"死了能不能自己回来"必须可见（此前完全不可见，用户只知道"要手动重启"）
  const wdEl = document.getElementById('st-wd');
  if (wdEl) {
    const wdS = KP.stateLabel('watchdog', st.watchdog, st.watchdog_pid);
    wdEl.textContent = wdS.text; wdEl.style.color = wdS.color;
  }
  const engS = KP.stateLabel('engine', st.engine, st.engine_pid);
  document.getElementById('st-eng').textContent = engS.text;
  document.getElementById('st-eng').style.color = engS.color;
  document.getElementById('st-port').textContent = st.port;
  document.getElementById('in-port').value = st.port;
  document.getElementById('st-ver').textContent = st.engine_version || '未知';
  document.getElementById('eng-cur').textContent = st.engine_version || '未知';
  // 版本来源自检（Phase 26）：版本号是从运行期记录还是包内读到的、是否刚自愈 ——
  // 「更新了但概览还是旧版本」这类谎报一眼可见（此前只能靠人对比 module.prop 与 /version）
  const verSrc = document.getElementById('ver-src');
  if (verSrc) verSrc.textContent = KP.engineVersionSourceLabel(st.engine_ver_src, st.engine_ver_healed === '1');
  document.getElementById('mod-cur').textContent = st.module_version || '未知';
  state.moduleVersion = (st.module_version || '').replace(/^v/, '').split('-r')[0];
  state.engineVersion = (st.engine_version || '').trim();
  state.modUrl = st.mod_url || KU.DEFAULT_MOD_UPDATE_URL;
  resources(st);
  renderAddrs(st);
  loadCurrentUpstreams(st);
  loadUpstreamEditor(st);
  // 出厂 key 卡片已移除（用户无感）：新装由 service.sh 开机自动补入；
  // 导入场景仪表盘走会话鉴权不再依赖 apiKeys 表（引擎 Phase 10 修复）
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
  box.innerHTML = rows.map(r =>
    `<div class="list-item"><span class="tag${r.label === '局域网' ? ' acc' : ''}">${r.label}</span>` +
    `<a style="flex:1;word-break:break-all;color:var(--acc)" href="${escAttr(r.url)}" target="_blank" rel="noopener">${esc(r.url)}</a>` +
    `<button data-u="${escAttr(r.url)}">复制</button></div>`).join('');
  box.querySelectorAll('button[data-u]').forEach(b => { b.onclick = () => copyText(b.dataset.u); });
}
function resources(st) {
  // 全部来自 ops.sh panel 单行输出，无需再起 shell
  const engKb = parseInt(st.engine_rss, 10) || 0;
  const dnsKb = parseInt(st.dns_rss, 10) || 0;
  const mi = { total: parseInt(st.mem_total, 10) || 0, avail: parseInt(st.mem_avail, 10) || 0 };
  const fmt = kb => !kb ? '-' : kb >= 1024 ? (kb / 1024).toFixed(1) + ' MB' : kb + ' kB';
  document.getElementById('mem-eng').textContent = fmt(engKb);
  document.getElementById('mem-eng').style.color = engKb > 307200 ? 'var(--err)' : engKb > 204800 ? 'var(--warn)' : '';
  const engBar = document.getElementById('bar-eng');
  engBar.style.width = Math.min(100, engKb / 3072) + '%';
  engBar.className = engKb > 307200 ? 'err' : engKb > 204800 ? 'warn' : '';
  document.getElementById('mem-dns').textContent = fmt(dnsKb);
  document.getElementById('bar-dns').style.width = Math.min(100, dnsKb / 3072) + '%';
  const sysPct = mi.total ? Math.round((mi.total - mi.avail) / mi.total * 100) : 0;
  document.getElementById('mem-sys').textContent = fmt(mi.avail) + ' / ' + fmt(mi.total);
  const sysBar = document.getElementById('bar-sys');
  sysBar.style.width = sysPct + '%';
  sysBar.className = mi.avail && mi.avail < 153600 ? 'err' : mi.avail && mi.avail < 307200 ? 'warn' : '';
}
async function restartAll() {
  return withBusy($id('btn-restart-all'), '重启中…', async () => {
    toast('重启中…（引擎最多等网络就绪 15 秒）');
    // 生命周期唯一入口：ops.sh restart-engine（内置等待，调用即知结果）
    const r = await KB.ops('restart-engine');
    const up = r.out.trim() === 'engine=up';
    await refresh();
    toast(up ? '✅ 已重启' : '❌ 引擎未拉起，请查看引擎日志', 4000);
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
    // 两条重启路径不该有不同的诚实度。
    const r = await KB.ops('restart-engine');
    const up = r.out.trim() === 'engine=up';
    await refresh();
    if (up) toast('✅ 引擎已在端口 ' + p + ' 重启', 3200);
    else toast('❌ 端口已写入 ' + p + '，但引擎未拉起（详见引擎日志）', 5000);
  });
}
async function startSvc() {
  // 用户显式启服务：清"停止"意图 + 拉起（唯一入口在 ops.sh → lifecycle 的 life_start_user）
  return withBusy($id('btn-start-svc'), '启动中…', async () => {
    // 必须判结果再报成功（2026-09-29 诊断）：life_start_user 现在如实回 engine=up/down
    // （过去它无论起没起来都回 started，界面于是无条件报「✅ 服务已启动」，
    //   与 refresh 后状态卡显示的"未运行"互相打脸）—— 同文件 restartAll/savePort 一直是判的。
    const r = await KB.ops('start-user');
    const up = r.out.trim() === 'engine=up';
    await refresh();
    toast(up ? '✅ 服务已启动' : '❌ 服务未启动（详见引擎日志）', up ? 3200 : 5000);
  });
}
async function stopSvc() {
  // 用户显式停服务：守护会尊重这个意图（不再"停了又自己回来"）
  return withBusy($id('btn-stop-svc'), '停止中…', async () => {
    const r = await KB.ops('stop-user');
    const ok = r.out.trim() === 'stopped';
    await refresh();
    if (ok) toast('已停止（守护不会自动拉起；点「启动服务」恢复）', 5200);
    else toast('❌ 停止失败（详见引擎日志）', 5000);
  });
}
$id('btn-refresh').onclick = refresh;
$id('btn-restart-all').onclick = restartAll;
$id('btn-start-svc').onclick = startSvc;
$id('btn-stop-svc').onclick = stopSvc;
$id('btn-port').onclick = savePort;
