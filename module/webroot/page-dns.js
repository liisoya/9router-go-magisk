// @ts-check
/* page-dns.js — DNS 页：转发器开关 / 上游编辑保存 / 逐个探测 / 候选池测速优选 / 回滚
 *
 * 所有写入都经 KB（bridge）命名操作，本文件不内联拼 shell。
 * 2026-09-28 从 app.js 纯搬迁（零行为变化）。
 */

const UPSTREAMS = CFG.DATA_DIR + '/dns-upstreams.conf';

// ── DNS 上游存储：写入/恢复的唯一出口 = ops.sh seam（2026-10-15 架构评审 C4）──
// 为什么收成一处：这套纪律原先整个在 JS 里（备份→原子写→热重载四步，本文件内四个动作
// 各写一遍），WebUI 经 ksu.exec 直写文件——绕过 ops.sh（AGENT-CONVENTIONS §3 运维动作
// 唯一入口），于是"备份→写→reload"在 JS 与 life_dns_recover 各有一份，adb/action 路径
// 想改上游也无门可入。现在改写只经 seam：ops.sh write-upstreams / restore-upstreams
// （备份用 lifecycle 的唯一实现 life_upstreams_backup_*，回滚点语义两侧同源）。
// 本文件只剩"要做什么 + verdict 怎么翻译成 UI"——次序与失败语义都在 seam 内。
// （回滚点路径 .initial / .prev 的知识随写入一起住在 seam：lifecycle.sh 的
//   life_upstreams_backup_* / ops.sh write-upstreams，页面不再知道文件名。）
const DNS_STORE = {
  /** 写入 + 备份 + 热重载（seam：ops.sh write-upstreams）。
   *  备份（.prev 每写必刷、.initial 首次）与原子写、热重载都在 seam 内；备份建不起来时
   *  seam **拒绝改写**（write-failed-no-backup）——用户配置不会被置于无回滚点的状态。
   *  返回：{ ok:false, verdict }=未写入（原因见 verdict）；
   *        { ok:true, reload }=已写入（reload=false 表示热重载没生效——调用方不得再报
   *        "已生效"，双 toast 互相打脸，2026-10-02）。 */
  async write(body) {
    const r = await KB.writeUpstreams(body);
    if (!r.ok) return { ok: false, verdict: r.verdict };
    return { ok: true, reload: r.reload };
  },
  /** 从回滚点恢复 + 必热重载（seam：ops.sh restore-upstreams）。which = 'initial' | 'prev'。 */
  async restore(which) {
    const r = await KB.restoreUpstreams(which);
    if (!r.ok) return { ok: false, verdict: r.verdict };
    return { ok: true, reload: r.reload };
  }
};
// 备份/热重载失败的 UI 翻译（verdict → 人话）：一处，四个动作共用。
// 为什么不再有 reloadDns()：热重载原是写之后 JS 再补一次 KB.ops('reload-dns')，四个调用点
// 全传 silent=true 让失败提示变成死代码（2026-09-29 诊断）；现在 reload 是 seam 写流程的
// 一步，成败由 verdict 带回（write-ok-reload-failed），UI 只认 verdict、不猜。
function dnsVerdictToast(w, okMsg) {
  if (!w.reload) { toast('⚠️ 配置已写入，但热重载失败（dnsfwd 未生效，详见 DNS 日志）', 4200); return false; }
  if (okMsg) toast(okMsg, 2400);
  return true;
}
// 候选池：单一事实源 = module/etc/dns-candidates（cat= label= 元数据 + 配置行）。
// 2026-10-22（ADR-0010 完整优选落地）：此前清单硬编码在 JS —— 面板芯片是它唯一消费者，
// 守护自动优选/手动优选（lifecycle/ops）想要候选池时没有第二份可共享，ADR-0010 因此挂账
// “待候选池收敛为单一来源”。现在收敛：本文件运行时读文件，shell 侧直接读同一文件，
// 加候选只改一处。读取失败回退**静态最小集**（= 明文两大条，够应急；不复制全量清单，
// 全量漂移由 tools/check-dns-candidates.sh 门禁看着）。
const DNS_CANDIDATES_FALLBACK = [
  { cat: '明文', label: '腾讯', v: 'nameserver 119.29.29.29' },
  { cat: '明文', label: '阿里', v: 'nameserver 223.6.6.6' }
];
const DNS_CANDIDATES_FILE = CFG.MODDIR + '/etc/dns-candidates';
async function loadDnsCandidates() {
  try {
    const r = await KB.readFile(DNS_CANDIDATES_FILE);
    if (!r.ok) return DNS_CANDIDATES_FALLBACK;
    const out = [];
    for (const raw of r.out.split('\n')) {
      const line = raw.trim();
      if (!line || line.startsWith('#')) continue;
      const catM = line.match(/(?:^|\s)cat=(\S+)/);
      const labelM = line.match(/(?:^|\s)label=(\S+)/);
      const v = KP.normUpstream(
        line.replace(/cat=\S+/g, '').replace(/label=\S+/g, '').trim());
      if (!v) continue;
      out.push({ cat: catM ? catM[1] : '明文', label: labelM ? labelM[1] : v, v });
    }
    return out.length ? out : DNS_CANDIDATES_FALLBACK;
  } catch { return DNS_CANDIDATES_FALLBACK; }
}
const DNS_CATS = ['明文', 'doh', 'dot'];
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
    return `<div class="list-item"><span class="${cls}">${t}</span><span class="grow">${esc(l.replace(/^(nameserver|doh|dot)\s/, ''))}</span></div>`;
  }).join('');
}
async function loadUpstreamEditor(st) {
  const el = document.getElementById('upstreams');
  el.value = st && st.upstreams_b64 ? b64Text(st.upstreams_b64)
    : (await KB.readFile(UPSTREAMS)).out;
  DNS_CANDIDATES = await loadDnsCandidates();   // 单一来源候选池（失败回退静态最小集）
  renderCandChips();
}
async function saveUpstreams() {
  return withBusy($id('btn-save'), '保存中…', async () => {
    const text = $id('upstreams').value;
    if (/127\.0\.0\.1|::1/.test(text)) { toast('❌ 禁止包含 127.0.0.1 / ::1（自我循环）', 3000); return; }
    if (!text.trim()) { toast('上游不能为空'); return; }
    // 写入经 seam（ops.sh write-upstreams）：备份/原子写/热重载都在里面；备份建不起来时
    // seam 拒绝改写（A6 的保护从"JS 预检"升级为"seam fail-closed"）。
    const w = await DNS_STORE.write(text);
    if (!w.ok) {
      toast(w.verdict === 'write-failed-no-backup'
        ? '⚠️ 备份失败（没有回滚点，不改写配置）' : '❌ 写入失败', 3600);
      return;
    }
    loadCurrentUpstreams();
    dnsVerdictToast(w);
  });
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
    const w = await DNS_STORE.restore('initial');
    if (!w.ok) { toast('没有初始默认备份（从未修改过）'); return; }
    if (dnsVerdictToast(w, '✅ 已恢复初始默认')) refresh();
  });
}
// ── 优选排序契约（2026-10-01：此前"按推荐还是按实测"没有明文）─────────────
// 问：优选该按**推荐顺序**（内置清单的排列）还是按**实测结果**？
// 答：以**本机实测**为准，推荐顺序不参与排序。三条，只写在这一处：
//   ① 排序的唯一实现是 KP.parseDnsProbeOutput（score = 可用率×100 − RTT/50 − fake-ip 罚 100，
//      降序）。内置清单 DNS_CANDIDATES 的排列只是**候选来源 + 芯片展示分组**（明文/DoH/DoT
//      三类），**不是优先级** —— 那份"可达清单"是 2026-09-30 别人机器上的实测，本机此刻
//      的 RTT / 可用率才是真的（跨网络、跨时间的推荐值一定会漂）。
//   ② 唯一例外：**自定义项（textarea 里手写的）无条件排在最前**，哪怕它没进 Top5。
//      理由：那是用户明示的意图，不能被一次自动优选悄悄淘汰。且 dnsfwd 运行时按健康度重排
//      （tools/dnsfwd.c 转发工作池："好多上游自然排前面、坏的上游沉底"），所以"写在第一行"
//      ≠ "每次都先查它"，这个例外不会让死上游长期占位。
//   ③ Top5 之外不写入：宁可少写，也不把没过门槛（可用率 ≥50% 且非 fake-ip）的塞进配置。
// 由 test/page-flows.test.js 的「优选排序」用例钉住 —— 改成按清单顺序会红。
async function optimize() {
  return withBusy($id('btn-opt'), '测速中…', async () => {
  const tbl = document.getElementById('opt-table');
  // 门禁①：没有任何可用上游 → 不得改写配置（planGate 阶段切片，2026-09-28 走查）
  // 候选池 = 用户自定义项（textarea）∪ 单一来源候选文件 ∪ 当前配置项；
  // 谁进池 ≠ 谁排前：排序只看 dnsfwd TSV 的 score 列（公式唯一实现在 dnsfwd.c probe_score）
  const custom = document.getElementById('upstreams').value.split('\n')
    .map(s => KP.normUpstream(s.trim())).filter(l => l && !l.startsWith('#'));
  const cur = (await KB.readFile(UPSTREAMS)).out.split('\n')
    .map(s => KP.normUpstream(s.trim())).filter(l => l && !l.startsWith('#'));
  const pool = await loadDnsCandidates();
  const cands = [...new Set([...custom, ...pool.map(c => KP.normUpstream(c.v)), ...cur])];
  const cf = CFG.DATA_DIR + '/dns-candidates.tmp';
  if (!await KB.writeFile(cf, cands.join('\n') + '\n')) {
    tbl.innerHTML = '<div class="hint">❌ 候选清单写入失败，未测速。</div>';
    return;
  }
  const r = await withElapsed(tbl, '测速中', () => KB.probeDns(cf, 60000));
  await KB.remove(cf);
  const rows = KP.parseDnsProbeOutput(r.out);
  // 门禁①：没有任何可用上游 → 不得改写配置（只求值本阶段门禁：planGate）
  const gRows = { ok: rows.length > 0, reason: '没有可用率 ≥50% 的上游' };
  if (!KP.planGate(KP.DNS_OPTIMIZE_PLAN, 'rows-gate', gRows).ok) {
    tbl.innerHTML = `<div class="hint">❌ ${gRows.reason}，保持原配置不动。</div>`;
    return;
  }
  const top = rows.slice(0, 5);
  tbl.innerHTML = '<table><tr><th>#</th><th>上游</th><th>类型</th><th>可用率</th><th>RTT</th><th>评分</th></tr>' +
    top.map((x, i) => `<tr><td>${i + 1}</td><td>${esc(x.upstream)}</td><td>${KP.upType(x.upstream)}</td><td>${Math.round(x.okRate * 100)}%</td><td>${x.rtt.toFixed(0)}ms</td><td><b>${x.score.toFixed(1)}</b></td></tr>`).join('') + '</table>' +
    '<div class="hint">✅ 已自动应用：手写的自定义项（保留在最前）+ 上方 Top5（按本机实测评分降序）。不满意可「回滚上一版」或「恢复初始默认」。</div>';
  // 自动应用：走 ops.sh optimize-dns（lifecycle.life_dns_reoptimize 唯一实现：
  // 候选池评分 → Top5 → 自定义项在前 → seam 写盘 + 热重载；此处不再重复编排）。
  // 上面已把候选与自定义项展示给用户；落盘内容可能与表格略有差异（reoptimize 会再次
  // 实测候选池），这是刻意收编：评分/写盘纪律只有 shell 一份实现。
  const v = await KB.optimizeDns();
  if (v === 'reoptimized') { toast('✅ 优选已应用（评分 Top5 已生效）', 2400); }
  else if (v === 'reoptimized-reload-failed') { toast('⚠️ 配置已写入，但热重载失败（详见 DNS 日志）', 4200); }
  else if (v.startsWith('beaten-strike=')) { toast(`ℹ️ 候选池击败当前配置（第 ${v.split('=')[1]} 次确认），再测一轮将自动应用`, 4200); }
  else if (v === 'not-beaten') { tbl.innerHTML += '<div class="hint">ℹ️ 候选池未能明显击败当前配置，未改写。</div>'; }
  else if (v === 'none-viable' || v === 'no-candidates') { toast('❌ 候选池没有可用上游，未改写', 3600); }
  else if (v === 'stale-config') { toast('ℹ️ 探测期间配置被修改过，本次优选让位', 3600); }
  else if (v === 'write-failed') { toast('❌ 优选写盘失败，原配置未动', 3600); }
  else { toast(`⚠️ 优选未生效（${v || '空输出'}）`, 4200); }
  loadCurrentUpstreams();
  loadUpstreamEditor();
  });
}
async function rollback() {
  return withBusy($id('btn-rollback'), '回滚中…', async () => {
    const w = await DNS_STORE.restore('prev');
    if (!w.ok) { toast('没有可回滚的备份'); return; }
    if (dnsVerdictToast(w, '✅ 已回滚上一版')) refresh();
  });
}
// （setBind/setBindUI/restartDns 已删除：index.html 无绑定范围按钮，死代码按 deletion test 清除）
// 候选清单缓存：loadUpstreamEditor 时填充（异步读单一来源文件），芯片渲染只消费缓存。
let DNS_CANDIDATES = DNS_CANDIDATES_FALLBACK;
function renderCandChips() {
  const box = document.getElementById('cand-chips');
  const cur = document.getElementById('upstreams').value;
  // 三类各起一行：类别名做行首标签，芯片只留区分词（"腾讯 / 阿里 / 字节 / 114"）。
  // 已加入的芯片靠 added 底色表示（判定=整行匹配 KP.hasUpstreamLine —— 子串误判见 parsers 注释）。
  box.innerHTML = DNS_CATS.map(cat => {
    const chips = DNS_CANDIDATES.filter(c => c.cat === cat).map(c => {
      const added = KP.hasUpstreamLine(cur, c.v);
      return `<span class="chip${added ? ' added' : ''}" data-v="${escAttr(c.v)}"` +
             ` title="${escAttr(c.v)}">${esc(c.label)}</span>`;
    }).join('');
    return `<div class="chipgroup"><span class="chips-k">${esc(cat)}</span>` +
           `<div class="chips">${chips}</div></div>`;
  }).join('');
  box.querySelectorAll('.chip').forEach(ch => {
    ch.onclick = () => toggleCandidate(ch.dataset.v);
  });
}
// 芯片点击的完整动作（2026-10-02 用户反馈：不要"记得手动保存"，要**自动生效**）：
// 切换编辑缓冲区 → 走 DNS_STORE 的备份→写盘→热重载次序，失败把缓冲区还原到切换前
// （界面与设备永远一致，不出现"看着改了其实没改"）。收成顶层函数：桩测试可直接驱动
// （renderCandChips 的芯片是 innerHTML 字符串，桩 DOM 的 querySelectorAll 驱动不了）。
// 在途位：写盘+热重载在途时再点会读到中间态 —— 芯片没有按钮可禁用，用位挡住。
let _toggleBusy = false;
async function toggleCandidate(v) {
  if (_toggleBusy) return;
  const ta = document.getElementById('upstreams');
  const prev = ta.value;
  const r = KP.toggleUpstreamLine(prev, v);
  _toggleBusy = true;
  try {
    // 移除后若配置里再无可生效行 → 拒绝（dnsfwd 拿空配置 = 域名解析全挂）
    if (r.removed && !r.text.split('\n').some(l => l.trim() && !l.trim().startsWith('#'))) {
      toast('至少保留一条 DNS 上游，未移除', 3200);
      return;
    }
    ta.value = r.text;
    renderCandChips();
    // 备份→写→热重载都在 seam（C4）；备份失败 seam 拒写（与 saveUpstreams 同一条纪律）
    const w = await DNS_STORE.write(r.text);
    if (!w.ok) {
      ta.value = prev; renderCandChips();
      toast(w.verdict === 'write-failed-no-backup'
        ? '⚠️ 备份失败（没有回滚点，未改写配置）' : '❌ 写入失败，已还原', 3200);
      return;
    }
    loadCurrentUpstreams();
    // 热重载失败时只警告、不报"已生效"（配置确实已写入，界面跟着配置走）
    dnsVerdictToast(w, r.removed ? '✅ 已移除并生效' : '✅ 已加入并生效');
  } finally {
    _toggleBusy = false;
  }
}
async function addUpstream() {
  const inp = document.getElementById('in-add');
  const v = KP.normUpstream(inp.value.trim());
  if (!v) { toast('请输入 DNS 地址'); return; }
  if (/127\.0\.0\.1|::1/.test(v)) { toast('❌ 禁止 127.0.0.1 / ::1'); return; }
  const ta = document.getElementById('upstreams');
  // 同一条整行判据（曾用子串，'223.5.5.5' 会被 doh URL 误判"已存在"）
  if (KP.hasUpstreamLine(ta.value, v)) { toast('已存在'); return; }
  // 手输添加与芯片同一套自动生效（toggleCandidate 会按 hasUpstreamLine 走加入分支）
  await toggleCandidate(v);
  inp.value = '';
}
// dnsfwd 开关：诚实回执走 runOpsAction（唯一实现，见 app-core.js）。
// 原先这两处各写一遍「比对状态词 → 挑 toast」，且失败文案与其它动作不一致（'状态：xxx'）。
async function dnsOn() {
  return runOpsAction({
    // 成功词集（started / running）由 KP.ACTION_WORDS 管：dnsfwd 本来就在跑时 shell 走
    // 幂等分支回 running，只认 started 会把它判成失败（2026-10-01 真机），词表即那起
    // 事故面的收口，这里不再手抄。
    btn: $id('btn-dns-on'), label: '开启中…', subcmd: 'enable-dns',
    okMsg: (r) => KP.isAlreadyRunning(r && r.out)
      ? '✅ dnsfwd 已在运行（无需重复开启）' : '✅ dnsfwd 已开启',
    failMsg: (r) => {
      // 同样是"不是失败但也不是 started"的状态，各有各的真相，不能一律叫失败
      switch (String((r && r.out) || '').trim()) {
        case 'yielded': return '⚠️ :53 已被其它 DNS 服务占用，dnsfwd 让位未启动';
        case 'off-by-user': return '⚠️ 服务已被停止，请先点「启动服务」';
        default: return '❌ dnsfwd 开启失败';
      }
    }
  });
}
async function dnsOff() {
  // 关闭的**成功路径**要再问一次 :53 有没有别的 DNS 服务 —— 这决定「引擎还能不能解析域名」，
  // 两句话的严重度差很远，所以 okMsg 用函数形态，由 runOpsAction 统一出回执。
  return runOpsAction({
    btn: $id('btn-dns-off'), label: '关闭中…', subcmd: 'stop-dns',
    okMsg: async () => {
      // 引擎的 Go 解析器只认 127.0.0.1:53（/etc/resolv.conf 在 Android 上不存在）。
      // 关闭后 :53 是否仍有 DNS 服务，决定模型域名解析是否正常 —— 如实告知，不粉饰。
      const busy = (await KB.ops('port53-busy')).out.trim() === '1';
      return busy
        ? '⛔ dnsfwd 已关闭。检测到 :53 仍有 DNS 服务在运行（如你自己的 DNS），引擎解析由它接管 ✓'
        : '⛔ dnsfwd 已关闭。⚠️ 设备 :53 无任何 DNS 服务——引擎域名解析会失败，模型将无法连接！请确保你的 DNS 服务监听 127.0.0.1:53，或重新开启 dnsfwd';
    },
    okMs: 6000,
    failMsg: '❌ dnsfwd 关闭失败'
  });
}
$id('btn-dns-on').onclick = dnsOn;
$id('btn-dns-off').onclick = dnsOff;

document.getElementById('btn-save').onclick = saveUpstreams;
document.getElementById('btn-probe').onclick = probe;
document.getElementById('btn-restore-init').onclick = restoreInit;
document.getElementById('btn-opt').onclick = optimize;
document.getElementById('btn-rollback').onclick = rollback;
document.getElementById('btn-add-upstream').onclick = addUpstream;

/** DNS 页的快照应用器（自注册，refresh 广播时调用）
 *  只碰 DNS 页自己的 DOM —— 概览不再知道 upstreams / cand-chips 的存在（2026-10-01 架构评审 #2）。 */
async function applyDns(st) {
  // 状态牌与概览**同形同文**（2026-09-30 用户要求两处一致）：底色与短形态都走 stateTile；
  // 完整句（含"（用户设置）"这类限定）挂到 title —— 两种粒度都留着，各归其位。
  const dnsLabel = KP.stateLabel('dns', st.dns, st.dns_pid);
  const dnsTile = KP.stateTile('dns', st.dns, st.dns_pid);
  const tile = $id('ro-dnsw');
  if (tile) { tile.className = 'ro ' + dnsTile.tone; tile.title = dnsLabel.text; }
  $id('dnsw-state').textContent = dnsTile.text;
  const mem = $id('mem-dnsw');
  if (mem) mem.textContent = KP.fmtMem(st.dns_rss);
  loadCurrentUpstreams(st);
  loadUpstreamEditor(st);
}
onPanelSnapshot(applyDns);
