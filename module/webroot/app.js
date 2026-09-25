/* app.js — UI 装配层：只做渲染与事件绑定。
 * 数据访问走 KBridge（root-shell 桥），解析走 KParsers（纯函数，可离线测试）。
 */
'use strict';
const CFG = window.CFG;
const KB = window.KBridge;
const KP = window.KParsers;

const UPSTREAMS = CFG.DATA_DIR + '/dns-upstreams.conf';
const BIND_FILE = CFG.DATA_DIR + '/dns-bind';
const DNS_PID = CFG.DATA_DIR + '/dnsfwd.pid';
const ENG_PID = CFG.DATA_DIR + '/9router.pid';
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
const modVersion = () => document.getElementById('mod-cur').textContent.replace(/^v/, '').split('-r')[0];

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
async function refresh() {
  let st;
  try {
    st = KP.parseOpsStatus((await KB.ops('status')).out);
  } catch (e) {
    document.getElementById('st-eng').textContent = '状态获取异常: ' + (e && e.message || e);
    return;
  }
  // 诊断探针：status 为空/缺 port 时，把原始取证信息直接显示在页面上
  if (!st.port) {
    const diag = document.getElementById('diag');
    diag.style.display = 'block';
    const idRes = await KB.sh('id');
    const rawRes = await KB.sh(`${CFG.MODDIR}/lib/ops.sh status 2>&1`);
    const lsRes = await KB.sh(`ls -la ${CFG.MODDIR}/lib/ 2>&1`);
    diag.textContent =
      '【诊断】ops.sh status 原始输出: ' + JSON.stringify(rawRes).slice(0, 300) +
      '\n【诊断】id: ' + esc(idRes.out.trim() || idRes.err.trim()) +
      '\n【诊断】lib/ 目录: ' + esc(lsRes.out.trim() || lsRes.err.trim());
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
  const engUp = st.engine === 'up';
  document.getElementById('st-eng').textContent = engUp ? '运行中 (PID ' + st.engine_pid + ')' : '未运行';
  document.getElementById('st-eng').style.color = engUp ? 'var(--ok)' : 'var(--err)';
  document.getElementById('st-port').textContent = st.port;
  document.getElementById('in-port').value = st.port;
  document.getElementById('st-ver').textContent = st.engine_version || '未知';
  document.getElementById('eng-cur').textContent = st.engine_version || '未知';
  document.getElementById('mod-cur').textContent = st.module_version || '未知';
  const mu = (await KB.sh(`cat ${MOD_UPDATE_URL_FILE} 2>/dev/null`)).out.trim() || DEFAULT_MOD_UPDATE_URL;
  window._modUrl = mu;
  resources(st);
  loadCurrentUpstreams();
  loadUpstreamEditor();
  checkFactoryKey(); // 自动维护出厂 key（幂等，用户无感）
}
async function resources(st) {
  const mi = KP.parseMeminfo((await KB.sh(`cat /proc/meminfo 2>/dev/null`)).out);
  const rssKb = async pid => pid ? KP.parseProcRss((await KB.sh(`cat /proc/${pid}/status 2>/dev/null`)).out) : 0;
  const engKb = (await rssKb(st.engine_pid)) || 0;
  const dnsKb = (await rssKb(st.dns_pid)) || 0;
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
  toast('重启中…（引擎最多等网络就绪 15 秒）');
  await KB.sh(`[ -f ${ENG_PID} ] && kill $(cat ${ENG_PID}) 2>/dev/null; [ -f ${DNS_PID} ] && kill $(cat ${DNS_PID}) 2>/dev/null; sleep 2; rm -f ${ENG_PID} ${DNS_PID}; sh ${CFG.MODDIR}/service.sh`);
  // 引擎启动含网络就绪等待（最长 15s）：轮询 20s 再判结果，避免误报"重启失败"
  let up = false;
  for (let i = 0; i < 10; i++) {
    await new Promise(r => setTimeout(r, 2000));
    if (KP.parseOpsStatus((await KB.ops('status')).out).engine === 'up') { up = true; break; }
  }
  await refresh();
  toast(up ? '✅ 已重启' : '❌ 20 秒内引擎未拉起，请查看引擎日志', 4000);
}
async function savePort() {
  const p = document.getElementById('in-port').value.trim();
  if (!/^[0-9]+$/.test(p) || +p < 1 || +p > 65535) { toast('端口必须是 1-65535 的数字'); return; }
  await KB.sh(`printf '%s\\n' '${p}' > ${PORT_FILE}`);
  toast('端口已写入 ' + p + '，重启引擎…');
  await KB.sh(`[ -f ${ENG_PID} ] && kill $(cat ${ENG_PID}) 2>/dev/null; sleep 1; rm -f ${ENG_PID}`);
  await KB.sh(`sh ${CFG.MODDIR}/service.sh`);
  setTimeout(refresh, 3000);
  toast('✅ 引擎已在端口 ' + p + ' 重启', 3200);
}
document.getElementById('btn-refresh').onclick = refresh;
document.getElementById('btn-restart-all').onclick = restartAll;
document.getElementById('btn-port').onclick = savePort;

// ═══════════ DNS ═══════════
async function loadCurrentUpstreams() {
  const box = document.getElementById('cur-upstreams');
  const conf = (await KB.sh(`cat ${UPSTREAMS} 2>/dev/null`)).out;
  const lines = conf.split('\n').map(s => s.trim()).filter(l => l && !l.startsWith('#'));
  if (!lines.length) { box.innerHTML = '<div class="hint">（空）</div>'; return; }
  box.innerHTML = lines.map(l => {
    const t = KP.upType(l);
    const cls = t === 'DoH' || t === 'DoT' ? 'tag acc' : 'tag';
    return `<div class="list-item"><span class="${cls}">${t}</span><span style="flex:1;word-break:break-all">${esc(l.replace(/^(nameserver|doh|dot)\s/, ''))}</span></div>`;
  }).join('');
}
async function loadUpstreamEditor() {
  document.getElementById('upstreams').value = (await KB.sh(`cat ${UPSTREAMS} 2>/dev/null`)).out;
  renderCandChips();
}
async function saveUpstreams() {
  const text = document.getElementById('upstreams').value;
  if (/127\.0\.0\.1|::1/.test(text)) { toast('❌ 禁止包含 127.0.0.1 / ::1（自我循环）', 3000); return; }
  if (!text.trim()) { toast('上游不能为空'); return; }
  // 首次修改前留存初始默认（"恢复初始默认"的回滚点）
  await KB.sh(`[ -f ${UPSTREAMS} ] && [ ! -f ${UPSTREAMS}.initial ] && cp ${UPSTREAMS} ${UPSTREAMS}.initial; cat > ${UPSTREAMS}.tmp <<'__EOF__'\n${text}\n__EOF__\nmv ${UPSTREAMS}.tmp ${UPSTREAMS}`);
  await reloadDns(true);
  loadCurrentUpstreams();
}
async function reloadDns(silent) {
  if ((await KB.ops('status')).out.match(/dns=up/)) {
    const r = await KB.sh(`kill -HUP $(cat ${DNS_PID}) && echo reloaded || echo fail`);
    if (!silent) toast(r.out.trim() === 'reloaded' ? '✅ 已热重载' : '❌ 热重载失败');
  } else if (!silent) toast('dnsfwd 未运行');
}
async function probe() {
  const out = document.getElementById('probe-out');
  out.style.display = 'block'; out.textContent = '探测中，约需数秒…';
  document.getElementById('btn-probe').disabled = true;
  const r = await KB.sh(`${CFG.MODDIR}/bin/dnsfwd -f ${UPSTREAMS} -P -j 8 2>&1`);
  out.textContent = r.out || r.err || '（无输出）';
  document.getElementById('btn-probe').disabled = false;
}
async function restoreInit() {
  const r = await KB.sh(`[ -f ${UPSTREAMS}.initial ] && cp ${UPSTREAMS}.initial ${UPSTREAMS} && echo ok || echo none`);
  if (r.out.trim() === 'ok') { await reloadDns(true); toast('✅ 已恢复初始默认'); refresh(); }
  else toast('没有初始默认备份（从未修改过）');
}
async function optimize() {
  const btn = document.getElementById('btn-opt');
  btn.disabled = true; btn.textContent = '测速中…';
  // 候选池 = 用户自定义项（textarea，最优先）∪ 内置候选清单 ∪ 当前配置项
  const custom = document.getElementById('upstreams').value.split('\n')
    .map(s => KP.normUpstream(s.trim())).filter(l => l && !l.startsWith('#'));
  const cur = (await KB.sh(`cat ${UPSTREAMS} 2>/dev/null`)).out.split('\n')
    .map(s => KP.normUpstream(s.trim())).filter(l => l && !l.startsWith('#'));
  const cands = [...new Set([...custom, ...DNS_CANDIDATES.map(c => KP.normUpstream(c.v)), ...cur])];
  const cf = CFG.DATA_DIR + '/dns-candidates.tmp';
  await KB.sh(`cat > ${cf} <<'__EOF__'\n${cands.join('\n')}\n__EOF__`);
  const r = await KB.sh(`${CFG.MODDIR}/bin/dnsfwd -f ${cf} -P -j 8 2>&1`);
  await KB.sh(`rm -f ${cf}`);
  btn.disabled = false; btn.textContent = '候选池测速';
  const rows = KP.parseDnsProbeOutput(r.out);
  const tbl = document.getElementById('opt-table');
  if (!rows.length) { tbl.innerHTML = '<div class="hint">❌ 没有可用率 ≥50% 的上游，保持原配置不动。</div>'; return; }
  const top = rows.slice(0, 5);
  tbl.innerHTML = '<table><tr><th>#</th><th>上游</th><th>类型</th><th>可用率</th><th>RTT</th><th>评分</th></tr>' +
    top.map((x, i) => `<tr><td>${i + 1}</td><td>${esc(x.upstream)}</td><td>${KP.upType(x.upstream)}</td><td>${Math.round(x.okRate * 100)}%</td><td>${x.rtt.toFixed(0)}ms</td><td><b>${x.score.toFixed(1)}</b></td></tr>`).join('') + '</table>' +
    '<div class="hint">✅ 已自动应用：自定义项（最优先）+ 上方 Top5。不满意可「回滚上一版」或「恢复初始默认」。</div>';
  // 自动应用：自定义项在前 + Top5，原子写入后热重载
  const merged = [...new Set([...custom, ...top.map(x => KP.normUpstream(x.upstream))])].join('\n');
  await KB.sh(`[ -f ${UPSTREAMS} ] && [ ! -f ${UPSTREAMS}.initial ] && cp ${UPSTREAMS} ${UPSTREAMS}.initial; [ -f ${UPSTREAMS} ] && cp ${UPSTREAMS} ${UPSTREAMS}.prev; cat > ${UPSTREAMS} <<'__EOF__'\n# 优选自动生成（自定义项在前） $(date)\n${merged}\n__EOF__`);
  await reloadDns(true);
  loadCurrentUpstreams();
  loadUpstreamEditor();
}
async function rollback() {
  const r = await KB.sh(`[ -f ${UPSTREAMS}.prev ] && cp ${UPSTREAMS}.prev ${UPSTREAMS} && echo ok || echo none`);
  if (r.out.trim() === 'ok') { await reloadDns(true); toast('✅ 已回滚上一版'); refresh(); }
  else toast('没有可回滚的备份');
}
function setBindUI(b) {
  document.getElementById('bind-loopback').className = b === 'loopback' ? 'on' : '';
  document.getElementById('bind-any').className = b === 'any' ? 'on' : '';
}
async function setBind(v) {
  await KB.sh(`printf '%s\\n' '${v}' > ${BIND_FILE}`);
  setBindUI(v);
  toast('已写入 ' + v + '，重启 dnsfwd 生效');
}
async function restartDns() {
  await KB.ops('stop-dns');
  const r = await KB.ops('start-dns');
  if (r.out.trim() === 'started') { toast('✅ dnsfwd 已重启'); setTimeout(refresh, 800); }
  else if (r.out.trim() === 'yielded') { toast('⚠️ :53 被其他进程占用，未重启', 3200); refresh(); }
  else if (r.out.trim() === 'disabled') { toast('dnsfwd 处于关闭状态（用上方开关开启）'); refresh(); }
  else toast('❌ 重启失败', 3200);
}
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
  toast(r.out.trim() === 'stopped' ? '⛔ dnsfwd 已关闭（引擎将改用设备已有 DNS 方案）' : '状态：' + r.out.trim(), 3200);
  refresh();
};
document.getElementById('btn-save').onclick = saveUpstreams;
document.getElementById('btn-probe').onclick = probe;
document.getElementById('btn-restore-init').onclick = restoreInit;
document.getElementById('btn-opt').onclick = optimize;
document.getElementById('btn-rollback').onclick = rollback;
document.getElementById('btn-add-upstream').onclick = addUpstream;

// ═══════════ 一致性检查 ═══════════
async function checkFactoryKey() {
  const dot = document.getElementById('dot-key');
  const btn = document.getElementById('btn-fix-key');
  const st = KP.parseOpsStatus((await KB.ops('status')).out);
  if (st.factory_key !== '0') {
    dot.className = 'dot ok';
    document.getElementById('ck-key').textContent = '存在 ✅';
    btn.style.display = 'none';
    return;
  }
  if (st.apikeys_total === '0') {
    // 表为空（全新安装）：自动补入，保证开箱即用
    await KB.ops('seed-key');
    dot.className = 'dot ok';
    document.getElementById('ck-key').textContent = '已自动补入 ✅';
    btn.style.display = 'none';
    return;
  }
  // 表非空但 key 缺失：多半是你在 Dashboard 有意删除 —— 不自动加回
  dot.className = 'dot warn';
  document.getElementById('ck-key').textContent = '已删除（不再自动补入）';
  btn.style.display = '';
}
document.getElementById('btn-fix-key').onclick = async () => {
  const r = await KB.ops('seed-key --force');
  toast(r.out.trim() === 'seeded' ? '✅ 已补入' : '状态：' + r.out.trim(), 3000);
  checkFactoryKey();
};
let orphanAliases = [];
async function scanOrphans() {
  const box = document.getElementById('orphan-list');
  const btn = document.getElementById('btn-clean-orphans');
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
  orphanAliases = KP.computeOrphans(aliases, live);
  if (!orphanAliases.length) {
    box.innerHTML = '<div class="hint">✅ 未发现孤儿数据</div>';
    return;
  }
  box.innerHTML = orphanAliases.map(a =>
    `<div class="list-item"><span class="tag err">孤儿</span><span style="flex:1;word-break:break-all">${esc(a)}</span></div>`).join('');
  btn.disabled = false; btn.style.display = '';
  btn.textContent = `确认清理孤儿（${orphanAliases.length} 项，一次全清）`;
}
async function cleanOrphans() {
  if (!orphanAliases.length) return;
  // 删除前二次确认：逐别名复查它是否真的不在存活节点/连接里
  // （扫描瞬间可能撞上引擎写事务导致误判——曾误删 Import from /models 刚导入的模型）
  const confirm = await KB.sqlFile(orphanAliases.map(a =>
    `SELECT '${a}' WHERE EXISTS (SELECT 1 FROM providerNodes WHERE id='${a}') OR EXISTS (SELECT 1 FROM providerConnections WHERE provider='${a}');`).join('\n'));
  const stillLive = confirm.out.split('\n').map(s => s.trim()).filter(Boolean);
  if (stillLive.length) {
    orphanAliases = orphanAliases.filter(a => !stillLive.includes(a));
    toast(`⚠️ ${stillLive.length} 项复查后确认仍存活，已从清理列表剔除`, 3600);
    scanOrphans();
    if (!orphanAliases.length) return;
  }
  const n = orphanAliases.length;
  // 删除前快照：把将被删除的行以 INSERT 语句形式存档，任何误删都可精确回滚
  const ts = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19);
  const like = orphanAliases.map(a => `key LIKE '${a}|%'`).join(' OR ');
  const eq = orphanAliases.map(a => `key='${a}'`).join(' OR ');
  const snap = await KB.sh(`mkdir -p ${CFG.DATA_DIR}/backups; ${CFG.MODDIR}/bin/sqlite3 ${CFG.DATA_DIR}/db/data.sqlite ".mode insert kv" "SELECT * FROM kv WHERE scope IN ('customModels','disabledModels') AND (${like.replace(/'/g, "''")} OR ${eq.replace(/'/g, "''")});" > ${CFG.DATA_DIR}/backups/kv-before-orphan-clean-${ts}.sql`);
  if (snap.err) { toast('⚠️ 快照失败，已中止删除（安全优先）', 3200); return; }
  // 单语句 OR 链式删除：一次点击全清（不逐条）
  await KB.sqlFile(`DELETE FROM kv WHERE scope='customModels' AND (${like}); DELETE FROM kv WHERE scope='disabledModels' AND (${eq});`);
  toast(`✅ 已一次性清理 ${n} 项（删除前快照已存 $DATA_DIR/backups/）`, 3600);
  orphanAliases = [];
  scanOrphans();
}
async function scanCred() {
  const box = document.getElementById('cred-list');
  box.innerHTML = '<div class="hint">扫描中…</div>';
  const conns = await KB.sqlFile(`SELECT id || '|' || provider || '|' || authType FROM providerConnections WHERE isActive=1;`);
  const rows = [];
  for (const line of conns.out.split('\n').map(s => s.trim()).filter(Boolean)) {
    const i1 = line.indexOf('|'), i2 = line.indexOf('|', i1 + 1);
    if (i1 < 0 || i2 < 0) continue;
    const id = line.slice(0, i1), provider = line.slice(i1 + 1, i2), authType = line.slice(i2 + 1);
    const d = await KB.sqlFile(`SELECT json_extract(data,'$.apiKey'), json_extract(data,'$.accessToken'), json_extract(data,'$.refreshToken') FROM providerConnections WHERE id='${id}';`);
    const parts = d.out.split('\n')[0].trim().split('|');
    const hasKey = parts[0] && parts[0] !== 'null';
    const hasTok = (parts[1] && parts[1] !== 'null') || (parts[2] && parts[2] !== 'null');
    if (!(authType === 'oauth' ? hasTok : hasKey)) rows.push({ provider, authType });
  }
  box.innerHTML = rows.length
    ? rows.map(r => `<div class="list-item"><span class="tag err">${esc(r.authType)}</span><span style="flex:1">${esc(r.provider)}</span></div>`).join('')
    : '<div class="hint">✅ 活跃连接凭据齐全</div>';
}
document.getElementById('btn-scan').onclick = scanOrphans;
document.getElementById('btn-clean-orphans').onclick = cleanOrphans;
document.getElementById('btn-scan-conn').onclick = scanCred;

// ═══════════ 更新 ═══════════
async function renderAccelCur() {
  const sel = (await KB.sh(`cat ${ACCEL_SEL} 2>/dev/null`)).out.trim();
  document.getElementById('accel-cur').textContent = sel || '直连 GitHub';
  return sel;
}
async function speedTest() {
  const btn = document.getElementById('btn-speed');
  btn.disabled = true; btn.textContent = '测速中…';
  const custom = (await KB.sh(`cat ${ACCEL_LIST} 2>/dev/null`)).out.split('\n').map(s => s.trim()).filter(Boolean);
  const all = [...new Set([...BUILTIN_ACCEL, ...custom])];
  const target = 'https://raw.githubusercontent.com/luqman-v1/9router-go/main/VERSION';
  const results = [];
  for (const node of all) {
    const r = await KB.sh(`curl -o /dev/null -s -m 8 -w '%{http_code} %{time_total}' '${node}${target}'`);
    const m = r.out.trim().match(/^(\d{3}) ([0-9.]+)$/);
    if (m && m[1] === '200') results.push({ node, ms: parseFloat(m[2]) * 1000 });
  }
  results.sort((a, b) => a.ms - b.ms);
  const top = results.slice(0, 5);
  const box = document.getElementById('accel-list');
  box.innerHTML = (top.length ? top.map((x, i) =>
    `<div class="list-item"><span class="tag">${i + 1}</span><span style="flex:1">${esc(x.node)}</span><span class="tag">${x.ms.toFixed(0)} ms</span><button data-u="${esc(x.node)}" class="pick2">选</button></div>`).join('')
    : '<div class="hint">所有节点都不可达，可直连或添加自定义节点</div>');
  box.querySelectorAll('.pick2').forEach(b => {
    b.onclick = async () => {
      await KB.sh(`printf '%s\\n' '${b.dataset.u}' > ${ACCEL_SEL}`);
      toast('已选中：' + esc(b.dataset.u)); renderAccelCur();
    };
  });
  btn.disabled = false; btn.textContent = '本机测速';
}
async function addAccel() {
  const u = prompt('自定义加速节点前缀（以 / 结尾，GitHub URL 会拼在后面）：');
  if (!u) return;
  await KB.sh(`printf '%s\\n' '${u.replace(/'/g, '')}' >> ${ACCEL_LIST}`);
  toast('已添加'); renderAccelCur();
}
async function clearAccel() {
  await KB.sh(`rm -f ${ACCEL_SEL}`);
  toast('已清除选中，直连 GitHub'); renderAccelCur();
}
document.getElementById('btn-speed').onclick = speedTest;
document.getElementById('btn-accel-add').onclick = addAccel;
document.getElementById('btn-accel-clear').onclick = clearAccel;

async function engCheck() {
  const out = document.getElementById('eng-out');
  out.style.display = 'block'; out.textContent = '检查中…';
  const p = (await KB.sh(`cat ${ACCEL_SEL} 2>/dev/null`)).out.trim();
  const r = await KB.sh(`curl -s -m 15 '${withAccel(ENGINE_VERSION_URL, p)}'`);
  let latest = '';
  try { latest = JSON.parse(r.out).latestVersion || ''; } catch {}
  if (!latest) { out.textContent = '❌ 无法获取上游版本（可先测速选择加速节点）'; return; }
  document.getElementById('eng-latest').textContent = latest;
  const cur = modVersion();
  out.textContent = `当前 ${cur} / 上游 ${latest}\n` + (KP.cmpVer(cur, latest) > 0 ? '有更新可用' : '已是最新');
  document.getElementById('btn-eng-update').disabled = KP.cmpVer(cur, latest) <= 0;
  window._engLatest = latest;
}
async function engUpdate() {
  const ver = window._engLatest; if (!ver) return;
  const out = document.getElementById('eng-out');
  out.textContent = '下载中（' + ((await KB.sh(`cat ${ACCEL_SEL} 2>/dev/null`)).out.trim() || '直连') + ')…';
  const p = (await KB.sh(`cat ${ACCEL_SEL} 2>/dev/null`)).out.trim();
  const base = `https://github.com/luqman-v1/9router-go/releases/download/${ver}`;
  const dl = await KB.sh(`curl -sL -m 300 -o /data/local/tmp/9r-eng.new '${withAccel(base + '/9router-go-linux-arm64', p)}' && echo dl-ok`);
  if (!dl.out.includes('dl-ok')) { out.textContent = '❌ 下载失败'; return; }
  out.textContent += '\n校验 SHA256…';
  const sum = await KB.sh(`curl -sL -m 60 '${withAccel(base + '/SHA256SUMS.txt', p)}' | grep '9router-go-linux-arm64'`);
  const expected = (sum.out.match(/^([0-9a-f]{64})/) || [])[1];
  if (expected) {
    const actual = (await KB.sh(`sha256sum /data/local/tmp/9r-eng.new`)).out.trim().split(' ')[0];
    if (actual !== expected) { out.textContent += `\n❌ SHA256 不匹配（${actual}），已放弃`; return; }
    out.textContent += '✅';
  } else out.textContent += '\n⚠️ 未取到校验和，跳过校验';
  out.textContent += '\n替换二进制并重启…';
  await KB.sh(`[ -f ${ENG_PID} ] && kill $(cat ${ENG_PID}) 2>/dev/null; sleep 1; rm -f ${ENG_PID}; cp ${CFG.MODDIR}/bin/9router-go ${CFG.MODDIR}/bin/9router-go.bak && mv /data/local/tmp/9r-eng.new ${CFG.MODDIR}/bin/9router-go && chmod 0755 ${CFG.MODDIR}/bin/9router-go`);
  await KB.sh(`sh ${CFG.MODDIR}/service.sh`);
  setTimeout(async () => { await refresh(); toast('✅ 引擎更新完成', 3200); out.textContent += '\n✅ 完成'; }, 3000);
}
async function modCheck() {
  const out = document.getElementById('mod-out');
  out.style.display = 'block'; out.textContent = '检查中…';
  const url = window._modUrl || DEFAULT_MOD_UPDATE_URL;
  const p = (await KB.sh(`cat ${ACCEL_SEL} 2>/dev/null`)).out.trim();
  const target = url.includes('github.com') || url.includes('raw.githubusercontent.com') ? withAccel(url, p) : url;
  const r = await KB.sh(`curl -sL -m 20 '${target}'`);
  let j; try { j = JSON.parse(r.out); } catch { out.textContent = '❌ 更新源不可达或格式错误\n' + r.out.slice(0, 200); return; }
  document.getElementById('mod-latest').textContent = (j.version || '?') + ' (code ' + j.versionCode + ')';
  const st = KP.parseOpsStatus((await KB.ops('status')).out);
  const curCode = parseInt((st.module_version || '').match(/r(\d+)$/)?.[1] || '0', 10);
  out.textContent = `当前 versionCode ${curCode} / 远端 ${j.versionCode}\n` + (j.versionCode > curCode ? '有更新可用' : '已是最新');
  document.getElementById('btn-mod-update').disabled = !(j.versionCode > curCode && j.zipUrl);
  window._modUpdate = j;
}
async function modUpdate() {
  const j = window._modUpdate; if (!j) return;
  const out = document.getElementById('mod-out');
  const p = (await KB.sh(`cat ${ACCEL_SEL} 2>/dev/null`)).out.trim();
  const dlUrl = /https?:\/\/(github\.com|raw\.githubusercontent\.com|objects\.githubusercontent\.com)\//.test(j.zipUrl) ? withAccel(j.zipUrl, p) : j.zipUrl;
  out.textContent = '下载模块 zip…';
  const dl = await KB.sh(`curl -sL -m 600 -o /data/local/tmp/mod-update.zip '${dlUrl}' && echo dl-ok`);
  if (!dl.out.includes('dl-ok')) { out.textContent = '❌ 下载失败'; return; }
  const chk = await KB.sh(`unzip -l /data/local/tmp/mod-update.zip`);
  if (!chk.out.includes('module.prop')) { out.textContent = '❌ zip 内容异常（缺 module.prop），已放弃'; return; }
  out.textContent += '\n停进程 → 解压覆盖 → 重启…';
  await KB.sh(`cp /data/local/tmp/mod-update.zip ${CFG.DATA_DIR}/last-module.zip; [ -f ${ENG_PID} ] && kill $(cat ${ENG_PID}) 2>/dev/null; [ -f ${DNS_PID} ] && kill $(cat ${DNS_PID}) 2>/dev/null; sleep 2; rm -f ${ENG_PID} ${DNS_PID}; cd ${CFG.MODDIR} && unzip -oq /data/local/tmp/mod-update.zip && chmod 0755 *.sh bin/*; rm -f /data/local/tmp/mod-update.zip; sh ${CFG.MODDIR}/service.sh`);
  setTimeout(async () => { await refresh(); toast('✅ 模块更新完成', 3200); out.textContent += '\n✅ 完成'; }, 4000);
}
async function setModUrl() {
  const u = prompt('模块更新源 URL（指向 update.json）：', window._modUrl || DEFAULT_MOD_UPDATE_URL);
  if (!u) return;
  await KB.sh(`printf '%s\\n' '${u.replace(/'/g, '')}' > ${MOD_UPDATE_URL_FILE}`);
  window._modUrl = u; toast('已保存');
}
document.getElementById('btn-eng-check').onclick = engCheck;
document.getElementById('btn-eng-update').onclick = engUpdate;
document.getElementById('btn-mod-check').onclick = modCheck;
document.getElementById('btn-mod-update').onclick = modUpdate;
document.getElementById('btn-mod-seturl').onclick = setModUrl;

refresh();
renderAccelCur();
