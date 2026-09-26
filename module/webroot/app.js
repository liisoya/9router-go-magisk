/* app.js — UI 装配层：只做渲染与事件绑定。
 * 数据访问走 KBridge（root-shell 桥），解析走 KParsers（纯函数，可离线测试）。
 */
'use strict';
const CFG = window.CFG;
const KB = window.KBridge;
const KP = window.KParsers;

const UPSTREAMS = CFG.DATA_DIR + '/dns-upstreams.conf';
const PORT_FILE = CFG.DATA_DIR + '/port';
const ACCEL_SEL = CFG.DATA_DIR + '/github-accel';
const ACCEL_LIST = CFG.DATA_DIR + '/accel-list.conf';
const MOD_UPDATE_URL_FILE = CFG.DATA_DIR + '/module-update-url';
const DEFAULT_MOD_UPDATE_URL = 'https://raw.githubusercontent.com/liisoya/9router-go-magisk/main/update.json';
const ENGINE_VERSION_URL = 'https://raw.githubusercontent.com/luqman-v1/9router-go/main/version.json';
// 候选池：旧版实测可达清单（国内明文 + 国内 DoH/DoT）
const DNS_CANDIDATES = [
  { v: 'nameserver 119.29.29.29',    note: '腾讯明文（旧版实测最快）' },
  { v: 'nameserver 223.6.6.6',       note: '阿里明文' },
  { v: 'nameserver 180.184.1.1',     note: '字节明文' },
  { v: 'nameserver 114.114.114.114', note: '114 明文兜底' },
  { v: 'doh https://223.5.5.5/dns-query',      note: '阿里 DoH（加密优先）' },
  { v: 'doh https://dns.alidns.com/dns-query', note: '阿里 DoH' },
  { v: 'doh https://doh.pub/dns-query',        note: '腾讯 DoH' },
  { v: 'doh https://doh.360.cn/dns-query',     note: '360 DoH' },
  { v: 'dot 223.5.5.5',              note: '阿里 DoT' }
];
// 初始加速清单来源：moretools.app/github-proxy 聚合（2026-09-25 测速可用前 15）
const BUILTIN_ACCEL = [
  'https://github.cnxiaobai.com/', 'https://gitproxy.mrhjx.cn/', 'https://github.chenc.dev/',
  'https://ghp.keleyaa.com/', 'https://ghproxy.xzhouqd.com/', 'https://github-proxy.memory-echoes.cn/',
  'https://gh.ddlc.top/', 'https://gh.dpik.top/', 'https://ghproxy.cxkpro.top/',
  'https://gh.padao.fun/', 'https://hub.ddayh.com/', 'https://gh.xxooo.cf/',
  'https://ghfile.geekertao.top/', 'https://github-proxy.lixxing.top/', 'https://gh-proxy.com/'
];

function toast(msg, ms) {
  const t = document.getElementById('toast');
  t.textContent = msg; t.classList.add('show');
  clearTimeout(t._tm); t._tm = setTimeout(() => t.classList.remove('show'), ms || 2400);
}
const esc = s => String(s == null ? '' : s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
const withAccel = (url, prefix) => prefix ? prefix + url : url;

// ── 跨函数状态收敛点（Phase 3：替代裸 window._modUrl/_modUpdate/_engLatest 与 orphanAliases）──
const state = {
  modUrl: DEFAULT_MOD_UPDATE_URL, // 模块更新源
  modUpdate: null,                // 远端 update.json 内容（modCheck → modUpdate）
  engLatest: '',                  // 引擎上游最新版本（engCheck → engUpdate）
  engineVersion: '',              // 引擎真实版本（panel → 概览/引擎更新比对）
  moduleVersion: '',              // 当前模块版本（refresh，不经 DOM 反解）
  orphans: []                     // 待清理孤儿别名（scanOrphans → cleanOrphans）
};

// ── 统一忙碌包装（Phase 3）：busy 态 / 异常 toast / finally 必然恢复按钮 ──
// 此前 optimize/speedTest 等手工 disable，一旦 await 链抛异常按钮永久卡死、UI 静默死亡。
async function withBusy(btn, busyLabel, fn) {
  const orig = btn ? btn.textContent : '';
  if (btn) { btn.disabled = true; if (busyLabel) btn.textContent = busyLabel; }
  try {
    return await fn();
  } catch (e) {
    console.error('[9r-panel]', e);
    toast('❌ 操作失败：' + (e && e.message || e), 4000);
  } finally {
    if (btn) { btn.disabled = false; btn.textContent = orig; }
  }
}
const $id = id => document.getElementById(id);

// ── 页签 ──
document.querySelectorAll('nav button').forEach(b => {
  b.onclick = () => {
    document.querySelectorAll('nav button').forEach(x => x.classList.remove('on'));
    document.querySelectorAll('.page').forEach(x => x.classList.remove('on'));
    b.classList.add('on');
    document.getElementById(b.dataset.page).classList.add('on');
  };
});

// ═══════════ 概览 ═══════════
// 一次 KB.ops('panel') 拿全概览页数据（status + meminfo + RSS + upstreams + 两个配置），
// 替代原先 9 次串行 root shell —— 每次 ksu.exec 都要新起 root shell 且全局串行排队，曾是首屏慢的根源。
const b64Text = KB.b64Decode; // upstreams_b64 解码（UTF-8 安全，实现收敛在 bridge）

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
    const lsRes = KB.sh(`ls -la ${CFG.MODDIR}/lib/ 2>&1`);
    Promise.all([idRes, rawRes, lsRes]).then(([idR, rawR, lsR]) => {
      diag.textContent =
        '【诊断】ops.sh status 原始输出: ' + JSON.stringify(rawR).slice(0, 300) +
        '\n【诊断】id: ' + esc(idR.out.trim() || idR.err.trim()) +
        '\n【诊断】lib/ 目录: ' + esc(lsR.out.trim() || lsR.err.trim());
    });
  } else {
    document.getElementById('diag').style.display = 'none';
  }
  const dnsDot = document.getElementById('dot-dns');
  let dnsTxt;
  if (st.dns === 'disabled') { dnsDot.className = 'dot err'; dnsTxt = '已关闭（用户设置）'; }
  else if (st.dns === 'up') { dnsDot.className = 'dot ok'; dnsTxt = '运行中 (PID ' + st.dns_pid + ')'; }
  else if (st.dns === 'yielded') { dnsDot.className = 'dot warn'; dnsTxt = '已让路（:53 被其他转发器占用）'; }
  else { dnsDot.className = 'dot err'; dnsTxt = '未运行'; }
  document.getElementById('st-dns').textContent = dnsTxt;
  document.getElementById('dnsw-state').textContent = dnsTxt;
  // 守护：引擎"死了能不能自己回来"必须可见（此前完全不可见，用户只知道"要手动重启"）
  const wdEl = document.getElementById('st-wd');
  if (wdEl) {
    if (st.watchdog === 'up') { wdEl.textContent = '运行中 (PID ' + st.watchdog_pid + ')'; wdEl.style.color = 'var(--ok)'; }
    else if (st.watchdog === 'stale') { wdEl.textContent = '未运行（已武装，下次重启生效）'; wdEl.style.color = 'var(--warn)'; }
    else { wdEl.textContent = '未启用'; wdEl.style.color = 'var(--warn)'; }
  }
  const engUp = st.engine === 'up';
  const engStopped = st.engine === 'stopped';   // 用户显式停服（守护尊重该意图，不会自动拉起）
  document.getElementById('st-eng').textContent = engUp ? '运行中 (PID ' + st.engine_pid + ')'
    : engStopped ? '已停止（用户设置）' : '未运行';
  document.getElementById('st-eng').style.color = engUp ? 'var(--ok)' : engStopped ? 'var(--warn)' : 'var(--err)';
  document.getElementById('st-port').textContent = st.port;
  document.getElementById('in-port').value = st.port;
  document.getElementById('st-ver').textContent = st.engine_version || '未知';
  document.getElementById('eng-cur').textContent = st.engine_version || '未知';
  document.getElementById('mod-cur').textContent = st.module_version || '未知';
  state.moduleVersion = (st.module_version || '').replace(/^v/, '').split('-r')[0];
  state.engineVersion = (st.engine_version || '').trim();
  state.modUrl = st.mod_url || DEFAULT_MOD_UPDATE_URL;
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
    `<a style="flex:1;word-break:break-all;color:var(--acc)" href="${esc(r.url)}" target="_blank" rel="noopener">${esc(r.url)}</a>` +
    `<button data-u="${esc(r.url)}">复制</button></div>`).join('');
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
    await KB.writeFile(PORT_FILE, p + '\n');
    toast('端口已写入 ' + p + '，重启引擎…');
    await KB.ops('restart-engine');
    await refresh();
    toast('✅ 引擎已在端口 ' + p + ' 重启', 3200);
  });
}
async function startSvc() {
  // 用户显式启服务：清"停止"意图 + 拉起（唯一入口在 ops.sh → lifecycle 的 life_start_user）
  return withBusy($id('btn-start-svc'), '启动中…', async () => {
    await KB.ops('start-user');
    await refresh();
    toast('✅ 服务已启动', 3200);
  });
}
async function stopSvc() {
  // 用户显式停服务：守护会尊重这个意图（不再"停了又自己回来"）
  return withBusy($id('btn-stop-svc'), '停止中…', async () => {
    await KB.ops('stop-user');
    await refresh();
    toast('已停止（守护不会自动拉起；点「启动服务」恢复）', 5200);
  });
}
$id('btn-refresh').onclick = refresh;
$id('btn-restart-all').onclick = restartAll;
$id('btn-start-svc').onclick = startSvc;
$id('btn-stop-svc').onclick = stopSvc;
$id('btn-port').onclick = savePort;

// ═══════════ DNS ═══════════
async function loadCurrentUpstreams(st) {
  const box = document.getElementById('cur-upstreams');
  // st 且带 upstreams_b64 → 用 panel 已带数据，零 shell；否则（如保存后刷新）按需 cat
  const conf = st && st.upstreams_b64 ? b64Text(st.upstreams_b64)
    : (await KB.readFile(UPSTREAMS)).out;
  const lines = conf.split('\n').map(s => s.trim()).filter(l => l && !l.startsWith('#'));
  if (!lines.length) { box.innerHTML = '<div class="hint">（空）</div>'; return; }
  box.innerHTML = lines.map(l => {
    const t = KP.upType(l);
    const cls = t === 'DoH' || t === 'DoT' ? 'tag acc' : 'tag';
    return `<div class="list-item"><span class="${cls}">${t}</span><span style="flex:1;word-break:break-all">${esc(l.replace(/^(nameserver|doh|dot)\s/, ''))}</span></div>`;
  }).join('');
}
async function loadUpstreamEditor(st) {
  const el = document.getElementById('upstreams');
  el.value = st && st.upstreams_b64 ? b64Text(st.upstreams_b64)
    : (await KB.readFile(UPSTREAMS)).out;
  renderCandChips();
}
async function saveUpstreams() {
  return withBusy($id('btn-save'), '保存中…', async () => {
    const text = $id('upstreams').value;
    if (/127\.0\.0\.1|::1/.test(text)) { toast('❌ 禁止包含 127.0.0.1 / ::1（自我循环）', 3000); return; }
    if (!text.trim()) { toast('上游不能为空'); return; }
    // 首次修改前留存初始默认（"恢复初始默认"的回滚点）
    await KB.backupOnce(UPSTREAMS, UPSTREAMS + '.initial');
    if (!await KB.writeFile(UPSTREAMS, text + '\n')) { toast('❌ 写入失败'); return; }
    await reloadDns(true);
    loadCurrentUpstreams();
  });
}
async function reloadDns(silent) {
  // dnsfwd 进程活性判断在 ops.sh（seam）内：pid 不在则返回 fail
  const r = await KB.ops('reload-dns');
  if (!silent) toast(r.out.trim() === 'reloaded' ? '✅ 已热重载' : '❌ 热重载失败');
}
async function probe() {
  return withBusy($id('btn-probe'), '探测中…', async () => {
    const out = $id('probe-out');
    out.style.display = 'block'; out.textContent = '探测中，约需数秒…';
    const r = await KB.probeDns(UPSTREAMS, 60000);
    out.textContent = r.out || r.err || '（无输出）';
  });
}
async function restoreInit() {
  return withBusy($id('btn-restore-init'), '恢复中…', async () => {
    const ok = await KB.restoreBackup(UPSTREAMS + '.initial', UPSTREAMS);
    if (ok) { await reloadDns(true); toast('✅ 已恢复初始默认'); refresh(); }
    else toast('没有初始默认备份（从未修改过）');
  });
}
async function optimize() {
  return withBusy($id('btn-opt'), '测速中…', async () => {
  // 候选池 = 用户自定义项（textarea，最优先）∪ 内置候选清单 ∪ 当前配置项
  const custom = document.getElementById('upstreams').value.split('\n')
    .map(s => KP.normUpstream(s.trim())).filter(l => l && !l.startsWith('#'));
  const cur = (await KB.readFile(UPSTREAMS)).out.split('\n')
    .map(s => KP.normUpstream(s.trim())).filter(l => l && !l.startsWith('#'));
  const cands = [...new Set([...custom, ...DNS_CANDIDATES.map(c => KP.normUpstream(c.v)), ...cur])];
  const cf = CFG.DATA_DIR + '/dns-candidates.tmp';
  await KB.writeFile(cf, cands.join('\n') + '\n');
  const r = await KB.probeDns(cf, 60000);
  await KB.remove(cf);
  const rows = KP.parseDnsProbeOutput(r.out);
  const tbl = document.getElementById('opt-table');
  if (!rows.length) { tbl.innerHTML = '<div class="hint">❌ 没有可用率 ≥50% 的上游，保持原配置不动。</div>'; return; }
  const top = rows.slice(0, 5);
  tbl.innerHTML = '<table><tr><th>#</th><th>上游</th><th>类型</th><th>可用率</th><th>RTT</th><th>评分</th></tr>' +
    top.map((x, i) => `<tr><td>${i + 1}</td><td>${esc(x.upstream)}</td><td>${KP.upType(x.upstream)}</td><td>${Math.round(x.okRate * 100)}%</td><td>${x.rtt.toFixed(0)}ms</td><td><b>${x.score.toFixed(1)}</b></td></tr>`).join('') + '</table>' +
    '<div class="hint">✅ 已自动应用：自定义项（最优先）+ 上方 Top5。不满意可「回滚上一版」或「恢复初始默认」。</div>';
  // 自动应用：自定义项在前 + Top5，原子写入后热重载
  const merged = [...new Set([...custom, ...top.map(x => KP.normUpstream(x.upstream))])].join('\n');
  await KB.backupOnce(UPSTREAMS, UPSTREAMS + '.initial');
  await KB.backupOnce(UPSTREAMS, UPSTREAMS + '.prev');
  await KB.writeFile(UPSTREAMS, '# 优选自动生成（自定义项在前）' + new Date().toLocaleString() + '\n' + merged + '\n');
  await reloadDns(true);
  loadCurrentUpstreams();
  loadUpstreamEditor();
  });
}
async function rollback() {
  return withBusy($id('btn-rollback'), '回滚中…', async () => {
    const ok = await KB.restoreBackup(UPSTREAMS + '.prev', UPSTREAMS);
    if (ok) { await reloadDns(true); toast('✅ 已回滚上一版'); refresh(); }
    else toast('没有可回滚的备份');
  });
}
// （setBind/setBindUI/restartDns 已删除：index.html 无绑定范围按钮，死代码按 deletion test 清除）
function renderCandChips() {
  const box = document.getElementById('cand-chips');
  const cur = document.getElementById('upstreams').value;
  box.innerHTML = DNS_CANDIDATES.map(c => {
    const added = cur.includes(c.v.replace(/^nameserver\s/, ''));
    return `<span class="chip${added ? ' added' : ''}" data-v="${esc(c.v)}">${added ? '✓' : '+'} ${esc(c.note)}</span>`;
  }).join('');
  box.querySelectorAll('.chip').forEach(ch => {
    ch.onclick = () => {
      const v = ch.dataset.v;
      const ta = document.getElementById('upstreams');
      if (ta.value.includes(v.replace(/^nameserver\s/, ''))) { toast('已在配置中'); return; }
      ta.value = (ta.value.trim() ? ta.value.trim() + '\n' : '') + v;
      renderCandChips();
      toast('已加入配置，记得「保存并热重载」');
    };
  });
}
async function addUpstream() {
  const inp = document.getElementById('in-add');
  const v = KP.normUpstream(inp.value.trim());
  if (!v) { toast('请输入 DNS 地址'); return; }
  if (/127\.0\.0\.1|::1/.test(v)) { toast('❌ 禁止 127.0.0.1 / ::1'); return; }
  const ta = document.getElementById('upstreams');
  if (ta.value.includes(v.replace(/^nameserver\s/, ''))) { toast('已存在'); return; }
  ta.value = (ta.value.trim() ? ta.value.trim() + '\n' : '') + v;
  inp.value = '';
  renderCandChips();
  toast('已加入配置，记得「保存并热重载」');
}
document.getElementById('btn-dns-on').onclick = async () => {
  const r = await KB.ops('enable-dns');
  toast(r.out.trim() === 'started' ? '✅ dnsfwd 已开启' : '状态：' + r.out.trim(), 3000);
  refresh();
};
document.getElementById('btn-dns-off').onclick = async () => {
  const r = await KB.ops('stop-dns');
  if (r.out.trim() !== 'stopped') { toast('状态：' + r.out.trim(), 3200); refresh(); return; }
  // 引擎的 Go 解析器只认 127.0.0.1:53（/etc/resolv.conf 在 Android 上不存在）。
  // 关闭后 :53 是否仍有 DNS 服务，决定模型域名解析是否正常——如实告知，不粉饰。
  const busy = (await KB.ops('port53-busy')).out.trim() === '1';
  if (busy) toast('⛔ dnsfwd 已关闭。检测到 :53 仍有 DNS 服务在运行（如你自己的 DNS），引擎解析由它接管 ✓', 4200);
  else toast('⛔ dnsfwd 已关闭。⚠️ 设备 :53 无任何 DNS 服务——引擎域名解析会失败，模型将无法连接！请确保你的 DNS 服务监听 127.0.0.1:53，或重新开启 dnsfwd', 6000);
  refresh();
};
document.getElementById('btn-save').onclick = saveUpstreams;
document.getElementById('btn-probe').onclick = probe;
document.getElementById('btn-restore-init').onclick = restoreInit;
document.getElementById('btn-opt').onclick = optimize;
document.getElementById('btn-rollback').onclick = rollback;
document.getElementById('btn-add-upstream').onclick = addUpstream;

// ═══════════ 一致性检查 ═══════════
// （出厂 key 检查/补入 UI 已移除，用户无感：service.sh 开机自动补入表空的
//   apiKeys；导入场景由引擎会话鉴权兜底（Phase 10）；用户主动删除不复活——
//   安全特性，外部 CLI 需要时在 Dashboard 的 keys 页手动添加）
async function scanOrphans() {
  return withBusy($id('btn-scan'), '扫描中…', async () => {
    const box = $id('orphan-list');
    const btn = $id('btn-clean-orphans');
    btn.disabled = true; btn.style.display = 'none';
    box.innerHTML = '<div class="hint">扫描中…</div>';
    const r = await KB.sqlFile(`SELECT id FROM providerNodes;
SELECT DISTINCT provider FROM providerConnections;
SELECT key FROM kv WHERE scope='customModels';
SELECT key FROM kv WHERE scope='disabledModels';`);
    const live = new Set(), aliases = KP.extractAliases(r.out.split('\n'));
    for (const l of r.out.split('\n').map(s => s.trim()).filter(Boolean)) {
      if (!l.includes('|')) live.add(l);
    }
    // 安全护栏：有模型别名却读不到任何节点/连接 = 扫描结果不可信（撞上引擎写事务等），
    // 绝不在这种状态下判定孤儿 —— 宁可扫不出，不可误删。
    if (aliases.size > 0 && live.size === 0) {
      box.innerHTML = '<div class="hint">⚠️ 扫描结果异常（未读到任何节点/连接，可能正被引擎写入占用），已中止判定。请稍后重试。</div>';
      return;
    }
    state.orphans = KP.computeOrphans(aliases, live);
    if (!state.orphans.length) {
      box.innerHTML = '<div class="hint">✅ 未发现孤儿数据</div>';
      return;
    }
    box.innerHTML = state.orphans.map(a =>
      `<div class="list-item"><span class="tag err">孤儿</span><span style="flex:1;word-break:break-all">${esc(a)}</span></div>`).join('');
    btn.disabled = false; btn.style.display = '';
    btn.textContent = `确认清理孤儿（${state.orphans.length} 项，一次全清）`;
  });
}
async function cleanOrphans() {
  if (!state.orphans.length) return;
  return withBusy($id('btn-clean-orphans'), '清理中…', async () => {
    // 删除前二次确认：逐别名复查它是否真的不在存活节点/连接里
    // （扫描瞬间可能撞上引擎写事务导致误判——曾误删 Import from /models 刚导入的模型）
    const confirm = await KB.sqlFile(state.orphans.map(a =>
      `SELECT '${a}' WHERE EXISTS (SELECT 1 FROM providerNodes WHERE id='${a}') OR EXISTS (SELECT 1 FROM providerConnections WHERE provider='${a}');`).join('\n'));
    const stillLive = confirm.out.split('\n').map(s => s.trim()).filter(Boolean);
    if (stillLive.length) {
      state.orphans = state.orphans.filter(a => !stillLive.includes(a));
      toast(`⚠️ ${stillLive.length} 项复查后确认仍存活，已从清理列表剔除`, 3600);
      scanOrphans();
      if (!state.orphans.length) return;
    }
    const n = state.orphans.length;
    // 删除前快照：把将被删除的行以 INSERT 语句形式存档，任何误删都可精确回滚
    const ts = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19);
    const like = state.orphans.map(a => `key LIKE '${a}|%'`).join(' OR ');
    const eq = state.orphans.map(a => `key='${a}'`).join(' OR ');
    const snapOk = await KB.sqlSnapshot(
      `SELECT * FROM kv WHERE scope IN ('customModels','disabledModels') AND (${like.replace(/'/g, "''")} OR ${eq.replace(/'/g, "''")});`,
      `${CFG.DATA_DIR}/backups/kv-before-orphan-clean-${ts}.sql`);
    if (!snapOk) { toast('⚠️ 快照失败，已中止删除（安全优先）', 3200); return; }
    // 单语句 OR 链式删除：一次点击全清（不逐条）
    await KB.sqlFile(`DELETE FROM kv WHERE scope='customModels' AND (${like}); DELETE FROM kv WHERE scope='disabledModels' AND (${eq});`);
    toast(`✅ 已一次性清理 ${n} 项（删除前快照已存 $DATA_DIR/backups/）`, 3600);
    state.orphans = [];
    scanOrphans();
  });
}
async function scanCred() {
  return withBusy($id('btn-scan-conn'), '扫描中…', async () => {
    const box = $id('cred-list');
    box.innerHTML = '<div class="hint">扫描中…</div>';
    // 单查询拿全凭据列（曾逐连接二次查询，N+1 次串行 root shell）
    const conns = await KB.sqlFile(
      `SELECT id || '|' || provider || '|' || authType || '|' || COALESCE(json_extract(data,'$.apiKey'),'null') || '|' || COALESCE(json_extract(data,'$.accessToken'),'null') || '|' || COALESCE(json_extract(data,'$.refreshToken'),'null') FROM providerConnections WHERE isActive=1;`);
    const rows = [];
    for (const line of conns.out.split('\n').map(s => s.trim()).filter(Boolean)) {
      const p = line.split('|');
      if (p.length < 6) continue;
      const [, provider, authType, apiKey, accessToken, refreshToken] = p;
      const hasKey = apiKey && apiKey !== 'null';
      const hasTok = (accessToken && accessToken !== 'null') || (refreshToken && refreshToken !== 'null');
      if (!(authType === 'oauth' ? hasTok : hasKey)) rows.push({ provider, authType });
    }
    box.innerHTML = rows.length
      ? rows.map(r => `<div class="list-item"><span class="tag err">${esc(r.authType)}</span><span style="flex:1">${esc(r.provider)}</span></div>`).join('')
      : '<div class="hint">✅ 活跃连接凭据齐全</div>';
  });
}
$id('btn-scan').onclick = scanOrphans;
$id('btn-clean-orphans').onclick = cleanOrphans;
$id('btn-scan-conn').onclick = scanCred;

// ═══════════ 更新 ═══════════
async function renderAccelCur(sel) {
  if (sel === undefined) sel = (await KB.readFile(ACCEL_SEL)).out.trim();
  document.getElementById('accel-cur').textContent = sel || '直连 GitHub';
  return sel;
}
async function speedTest() {
  return withBusy($id('btn-speed'), '测速中…', async () => {
    const custom = (await KB.readFile(ACCEL_LIST)).out.split('\n').map(s => s.trim()).filter(Boolean);
    const all = [...new Set([...BUILTIN_ACCEL, ...custom])];
    const target = 'https://raw.githubusercontent.com/luqman-v1/9router-go/main/VERSION';
    const results = [];
    for (const node of all) {
      const r = await KB.curlTiming(node + target);
      if (r.ok) results.push({ node, ms: r.ms });
    }
    results.sort((a, b) => a.ms - b.ms);
    const top = results.slice(0, 5);
    const box = document.getElementById('accel-list');
    box.innerHTML = (top.length ? top.map((x, i) =>
      `<div class="list-item"><span class="tag">${i + 1}</span><span style="flex:1">${esc(x.node)}</span><span class="tag">${x.ms.toFixed(0)} ms</span><button data-u="${esc(x.node)}" class="pick2">选</button></div>`).join('')
      : '<div class="hint">所有节点都不可达，可直连或添加自定义节点</div>');
    box.querySelectorAll('.pick2').forEach(b => {
      b.onclick = async () => {
        await KB.writeFile(ACCEL_SEL, b.dataset.u + '\n');
        toast('已选中：' + esc(b.dataset.u)); renderAccelCur();
      };
    });
  });
}
async function addAccel() {
  const u = prompt('自定义加速节点前缀（以 / 结尾，GitHub URL 会拼在后面）：');
  if (!u) return;
  await KB.appendLine(ACCEL_LIST, u); // 完整保留 URL（曾 strip 单引号破坏含引号 URL）
  toast('已添加'); renderAccelCur();
}
async function clearAccel() {
  await KB.remove(ACCEL_SEL);
  toast('已清除选中，直连 GitHub'); renderAccelCur();
}
document.getElementById('btn-speed').onclick = speedTest;
document.getElementById('btn-accel-add').onclick = addAccel;
document.getElementById('btn-accel-clear').onclick = clearAccel;

async function engCheck() {
  return withBusy($id('btn-eng-check'), '检查中…', async () => {
    const out = $id('eng-out');
    out.style.display = 'block'; out.textContent = '检查中…';
    const p = (await KB.readFile(ACCEL_SEL)).out.trim();
    const r = await KB.fetch(withAccel(ENGINE_VERSION_URL, p), 15);
    let latest = '';
    try { latest = JSON.parse(r.out).latestVersion || ''; } catch {}
    if (!latest) { out.textContent = '❌ 无法获取上游版本（可先测速选择加速节点）'; return; }
    document.getElementById('eng-latest').textContent = latest;
    // 引擎当前版本用真实来源（panel 的 engine_version），绝不拿模块版本冒充
    const cur = state.engineVersion;
    if (!cur) { out.textContent = `上游 ${latest}\n⚠️ 本地引擎版本未知（旧版模块安装，重装/更新模块后可显示）——是否更新请自行判断`; return; }
    out.textContent = `当前 ${cur} / 上游 ${latest}\n` + (KP.cmpVer(cur, latest) > 0 ? '有更新可用' : '已是最新');
    document.getElementById('btn-eng-update').disabled = KP.cmpVer(cur, latest) <= 0;
    state.engLatest = latest;
  });
}
async function engUpdate() {
  const ver = state.engLatest; if (!ver) return;
  const out = document.getElementById('eng-out');
  const p = (await KB.readFile(ACCEL_SEL)).out.trim();
  out.textContent = '下载中（' + (p || '直连') + '）…';
  const base = `https://github.com/luqman-v1/9router-go/releases/download/${ver}`;
  if (!await KB.download(withAccel(base + '/9router-go-linux-arm64', p), '/data/local/tmp/9r-eng.new', 300)) {
    out.textContent = '❌ 下载失败'; return;
  }
  out.textContent += '\n校验 SHA256…';
  const sum = await KB.fetch(withAccel(base + '/SHA256SUMS.txt', p), 60);
  const sumLine = sum.out.split('\n').find(l => l.includes('9router-go-linux-arm64')) || '';
  const expected = (sumLine.match(/^([0-9a-f]{64})/) || [])[1];
  if (expected) {
    const actual = await KB.sha256('/data/local/tmp/9r-eng.new');
    if (actual !== expected) { out.textContent += `\n❌ SHA256 不匹配（${actual}），已放弃`; return; }
    out.textContent += '✅';
  } else out.textContent += '\n⚠️ 未取到校验和，跳过校验';
  out.textContent += '\n替换二进制并重启…';
  // 安装唯一入口：备份 → 替换 → 权限 → 记录版本 → 重启，全在 ops.sh seam 内
  const r = await KB.ops(`install-engine /data/local/tmp/9r-eng.new ${ver}`);
  if (r.out.trim() !== 'engine=up') { out.textContent += `\n❌ 安装/重启失败：${r.out.trim()}`; return; }
  await refresh();
  toast('✅ 引擎更新完成', 3200); out.textContent += '\n✅ 完成';
}
async function modCheck() {
  return withBusy($id('btn-mod-check'), '检查中…', async () => {
    const out = $id('mod-out');
    out.style.display = 'block'; out.textContent = '检查中…';
    const url = state.modUrl || DEFAULT_MOD_UPDATE_URL;
    const p = (await KB.readFile(ACCEL_SEL)).out.trim();
    const target = url.includes('github.com') || url.includes('raw.githubusercontent.com') ? withAccel(url, p) : url;
    const r = await KB.fetch(target, 20);
    let j; try { j = JSON.parse(r.out); } catch { out.textContent = '❌ 更新源不可达或格式错误\n' + r.out.slice(0, 200); return; }
    document.getElementById('mod-latest').textContent = (j.version || '?') + ' (code ' + j.versionCode + ')';
    // versionCode 用 module.prop 真实字段——曾从 "v1.9.1-r1" 正则提取 r1=1
    // 与远端 109010 比较，导致没发新版也永远提示有更新
    const st = KP.parseOpsStatus((await KB.ops('status')).out);
    const curCode = parseInt(st.versioncode || '0', 10);
    out.textContent = `当前 versionCode ${curCode} / 远端 ${j.versionCode}\n` + (j.versionCode > curCode ? '有更新可用' : '已是最新');
    document.getElementById('btn-mod-update').disabled = !(j.versionCode > curCode && j.zipUrl);
    state.modUpdate = j;
  });
}
async function modUpdate() {
  const j = state.modUpdate; if (!j) return;
  const out = document.getElementById('mod-out');
  const p = (await KB.readFile(ACCEL_SEL)).out.trim();
  const dlUrl = /https?:\/\/(github\.com|raw\.githubusercontent\.com|objects\.githubusercontent\.com)\//.test(j.zipUrl) ? withAccel(j.zipUrl, p) : j.zipUrl;
  out.textContent = '下载模块 zip…';
  if (!await KB.download(dlUrl, '/data/local/tmp/mod-update.zip', 600)) { out.textContent = '❌ 下载失败'; return; }
  const chk = await KB.zipList('/data/local/tmp/mod-update.zip');
  if (!chk.out.includes('module.prop')) { out.textContent = '❌ zip 内容异常（缺 module.prop），已放弃'; return; }
  out.textContent += '\n停进程 → 解压覆盖 → 重启…';
  // 安装唯一入口：备份 → 解压 → chmod 兜底（含 lib/）→ 清理 → 重启，全在 ops.sh seam 内
  const r = await KB.ops('install-module /data/local/tmp/mod-update.zip');
  if (r.out.trim() !== 'engine=up') { out.textContent += `\n❌ 安装/重启失败：${r.out.trim()}`; return; }
  await refresh();
  toast('✅ 模块更新完成', 3200); out.textContent += '\n✅ 完成';
}
async function setModUrl() {
  const u = prompt('模块更新源 URL（指向 update.json）：', state.modUrl || DEFAULT_MOD_UPDATE_URL);
  if (!u) return;
  await KB.writeFile(MOD_UPDATE_URL_FILE, u + '\n');
  state.modUrl = u; toast('已保存');
}
$id('btn-eng-check').onclick = engCheck;
$id('btn-eng-update').onclick = engUpdate;
$id('btn-mod-check').onclick = modCheck;
$id('btn-mod-update').onclick = modUpdate;
$id('btn-mod-seturl').onclick = setModUrl;

refresh().then(st => renderAccelCur(st && st.accel_sel));
