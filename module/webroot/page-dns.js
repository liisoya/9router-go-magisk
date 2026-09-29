// @ts-check
/* page-dns.js — DNS 页：转发器开关 / 上游编辑保存 / 逐个探测 / 候选池测速优选 / 回滚
 *
 * 所有写入都经 KB（bridge）命名操作，本文件不内联拼 shell。
 * 2026-09-28 从 app.js 纯搬迁（零行为变化）。
 */

const UPSTREAMS = CFG.DATA_DIR + '/dns-upstreams.conf';
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
const b64Text = KB.b64Decode; // upstreams_b64 解码（UTF-8 安全，实现收敛在 bridge）
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
    // 首次修改前留存初始默认（"恢复初始默认"的回滚点）。
    // 必须**先确认回滚点可用、再改写配置**（2026-09-29 架构走查 A6）：过去丢弃 backupOnce 的
    // 返回值照样写 —— 备份失败时用户配置被覆盖、却没有 .initial 可回滚；而同页 optimize()
    // 一直把"回滚点可用"当作改写的前置（同一份安全知识只对了一半，这里对齐）。
    if (!await KB.backupOnce(UPSTREAMS, UPSTREAMS + '.initial')) {
      toast('⚠️ 备份失败（没有回滚点，不改写配置）', 3600);
      return;
    }
    if (!await KB.writeFile(UPSTREAMS, text + '\n')) { toast('❌ 写入失败'); return; }
    await reloadDns(true);
    loadCurrentUpstreams();
  });
}
async function reloadDns(silent) {
  // dnsfwd 进程活性判断在 ops.sh（seam）内：pid 不在则返回 fail
  const r = await KB.ops('reload-dns');
  const ok = r.out.trim() === 'reloaded';
  // 失败**永远要报**（2026-09-29 诊断）：失败提示原本写在 `if (!silent)` 里，而四个调用点
  // （保存/优选/恢复/回滚）**全部**传 true → 那是死代码 → 配置已写盘但 dnsfwd 没生效时，
  // 界面照样报「✅ 已恢复 / 已回滚 / 已自动应用」。silent 的语义收窄成"成功时不打扰"。
  if (!ok) {
    toast('⚠️ 配置已写入，但热重载失败（dnsfwd 未生效，详见 DNS 日志）', 4200);
    return false;
  }
  if (!silent) toast('✅ 已热重载');
  return true;
}
async function probe() {
  return withBusy($id('btn-probe'), '探测中…', async () => {
    const out = $id('probe-out');
    // 单次 dnsfwd -P 最长 60s，而 promise 形态拿不到中途输出 → 用"已用时长"心跳代替
    // （A5：点下去像卡死）
    const r = await withElapsed(out, '探测中', () => KB.probeDns(UPSTREAMS, 60000));
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
  if (!await KB.writeFile(cf, cands.join('\n') + '\n')) {
    document.getElementById('opt-table').innerHTML = '<div class="hint">❌ 候选清单写入失败，未测速。</div>';
    return;
  }
  const r = await withElapsed(document.getElementById('opt-table'), '测速中', () => KB.probeDns(cf, 60000));
  await KB.remove(cf);
  const rows = KP.parseDnsProbeOutput(r.out);
  const tbl = document.getElementById('opt-table');
  // 门禁①：没有任何可用上游 → 不得改写配置（只求值本阶段门禁：planGate，
  // 2026-09-28 走查证实 planSteps 整计划求值会在此假拦 —— 活体 bug）
  const gRows = { ok: rows.length > 0, reason: '没有可用率 ≥50% 的上游' };
  if (!KP.planGate(KP.DNS_OPTIMIZE_PLAN, 'rows-gate', gRows).ok) {
    tbl.innerHTML = `<div class="hint">❌ ${gRows.reason}，保持原配置不动。</div>`;
    return;
  }
  const top = rows.slice(0, 5);
  tbl.innerHTML = '<table><tr><th>#</th><th>上游</th><th>类型</th><th>可用率</th><th>RTT</th><th>评分</th></tr>' +
    top.map((x, i) => `<tr><td>${i + 1}</td><td>${esc(x.upstream)}</td><td>${KP.upType(x.upstream)}</td><td>${Math.round(x.okRate * 100)}%</td><td>${x.rtt.toFixed(0)}ms</td><td><b>${x.score.toFixed(1)}</b></td></tr>`).join('') + '</table>' +
    '<div class="hint">✅ 已自动应用：自定义项（最优先）+ 上方 Top5。不满意可「回滚上一版」或「恢复初始默认」。</div>';
  // 自动应用：自定义项在前 + Top5，原子写入后热重载
  const merged = [...new Set([...custom, ...top.map(x => KP.normUpstream(x.upstream))])].join('\n');
  // 门禁②：先留回滚点；回滚点不可用就**不改写**（backupOnce 现在诚实返回，不再是永远 true）
  const okInitial = await KB.backupOnce(UPSTREAMS, UPSTREAMS + '.initial');
  // `.prev` 必须**每次刷新**（2026-09-29 诊断）：backupOnce 的语义是"目标已存在就跳过（echo exists）"，
  // 直接复用会让 .prev 永远停在"第一次优选前"那份。而 index.html 承诺的是「上一版存 .prev 可回滚」，
  // 于是「回滚上一版」实际回到首版（常等于 .initial），用户丢掉最近一次优选前的状态 ——
  // 优选是自动应用的，回滚正是它唯一的安全网。先删旧的，backupOnce 才会真正写入本版。
  await KB.remove(UPSTREAMS + '.prev');
  const okPrev = await KB.backupOnce(UPSTREAMS, UPSTREAMS + '.prev');
  const gBackup = { ok: okInitial && okPrev, reason: '备份失败（没有回滚点，不改写配置）' };
  if (!KP.planGate(KP.DNS_OPTIMIZE_PLAN, 'backup-gate', gBackup).ok) {
    toast(`⚠️ ${gBackup.reason}`, 3200);
    return;
  }
  if (!await KB.writeFile(UPSTREAMS, '# 优选自动生成（自定义项在前）' + new Date().toLocaleString() + '\n' + merged + '\n')) {
    toast('❌ 配置写入失败，原配置未动（备份仍在）', 3600); return;
  }
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
    return `<span class="chip${added ? ' added' : ''}" data-v="${escAttr(c.v)}">${added ? '✓' : '+'} ${esc(c.note)}</span>`;
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
