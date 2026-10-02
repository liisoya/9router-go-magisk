// @ts-check
/* page-dns.js — DNS 页：转发器开关 / 上游编辑保存 / 逐个探测 / 候选池测速优选 / 回滚
 *
 * 所有写入都经 KB（bridge）命名操作，本文件不内联拼 shell。
 * 2026-09-28 从 app.js 纯搬迁（零行为变化）。
 */

const UPSTREAMS = CFG.DATA_DIR + '/dns-upstreams.conf';

// ── DNS 上游存储：备份点语义 + 「写前必备份、备份失败不改写、写后必热重载」的次序 ──
// 为什么收成一处（2026-10-01 架构评审 #7）：这套次序原先散在 save / optimize / restoreInit /
// rollback 四个动作里各写一遍，其中「.prev 必须先删再备份」只由一条**源码顺序扫描**守着
// （page-flows.test.js 的 B1b）—— 正是"真 bug 藏在调用方式里"的形态。
// 现在四个动作只说"我要做什么"，次序与两个回滚点的语义都在这里。
const DNS_INITIAL = UPSTREAMS + '.initial';   // 首次修改前的初始默认（「恢复初始默认」的回滚点）
const DNS_PREV = UPSTREAMS + '.prev';         // 每次改写前的上一版（「回滚上一版」的回滚点）
const DNS_STORE = {
  /** 「恢复初始默认」的回滚点：backupOnce 语义 = **目标已存在就跳过**。返回是否可用。 */
  ensureInitial: () => KB.backupOnce(UPSTREAMS, DNS_INITIAL),
  /** 「回滚上一版」的回滚点：**必须每次刷新**。
   *  为什么不能直接复用 backupOnce：它是"目标已存在就跳过"，.prev 会永远停在
   *  "第一次优选前"那份，而界面承诺的是「上一版存 .prev 可回滚」——
   *  于是「回滚上一版」实际回到首版，用户丢掉最近一次优选前的状态。
   *  优选是自动应用的，回滚正是它唯一的安全网。先删旧的，backupOnce 才会真正写入本版。 */
  async refreshPrev() { await KB.remove(DNS_PREV); return KB.backupOnce(UPSTREAMS, DNS_PREV); },
  /** 写入 + **必热重载**。前置：回滚点已由调用方确认可用（ensureInitial / refreshPrev）。 */
  async write(body) {
    if (!await KB.writeFile(UPSTREAMS, body + '\n')) return false;
    await reloadDns(true);
    return true;
  },
  /** 从某个回滚点恢复 + **必热重载** */
  async restore(from) {
    const ok = await KB.restoreBackup(from, UPSTREAMS);
    if (ok) await reloadDns(true);
    return ok;
  }
};
// 候选池：旧版实测可达清单（国内明文 + 国内 DoH/DoT）。
// 2026-09-30：改为按**三大类**（明文 / DoH / DoT）分组展示，标签只留最短的区分词 ——
// 原来九个芯片平铺、每个还带一句括号备注，一屏读不出结构（用户反馈"很乱"）。
// `v` 是唯一事实（写盘与优选都用它）；`cat`/`label` 只服务展示，
// 完整地址不另存一份文案，由 v 现取进 title（存两份一定会漂）。
const DNS_CANDIDATES = [
  { cat: '明文', label: '腾讯',     v: 'nameserver 119.29.29.29' },
  { cat: '明文', label: '阿里',     v: 'nameserver 223.6.6.6' },
  { cat: '明文', label: '字节',     v: 'nameserver 180.184.1.1' },
  { cat: '明文', label: '114',      v: 'nameserver 114.114.114.114' },
  { cat: 'DoH',  label: '阿里',     v: 'doh https://223.5.5.5/dns-query' },
  { cat: 'DoH',  label: '阿里域名', v: 'doh https://dns.alidns.com/dns-query' },
  { cat: 'DoH',  label: '腾讯',     v: 'doh https://doh.pub/dns-query' },
  { cat: 'DoH',  label: '360',      v: 'doh https://doh.360.cn/dns-query' },
  { cat: 'DoT',  label: '阿里',     v: 'dot 223.5.5.5' }
];
const DNS_CATS = ['明文', 'DoH', 'DoT'];
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
  renderCandChips();
}
async function saveUpstreams() {
  return withBusy($id('btn-save'), '保存中…', async () => {
    const text = $id('upstreams').value;
    if (/127\.0\.0\.1|::1/.test(text)) { toast('❌ 禁止包含 127.0.0.1 / ::1（自我循环）', 3000); return; }
    if (!text.trim()) { toast('上游不能为空'); return; }
    // 前置：回滚点可用才改写（A6）。次序知识在 DNS_STORE，这里只说"要做什么"。
    if (!await DNS_STORE.ensureInitial()) {
      toast('⚠️ 备份失败（没有回滚点，不改写配置）', 3600);
      return;
    }
    if (!await DNS_STORE.write(text)) { toast('❌ 写入失败'); return; }
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
    const ok = await DNS_STORE.restore(DNS_INITIAL);
    if (ok) { toast('✅ 已恢复初始默认'); refresh(); }
    else toast('没有初始默认备份（从未修改过）');
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
  // 候选池 = 用户自定义项（textarea）∪ 内置候选清单 ∪ 当前配置项；
  // 谁进池 ≠ 谁排前：排序只看实测评分（见上方契约①）
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
    '<div class="hint">✅ 已自动应用：手写的自定义项（保留在最前）+ 上方 Top5（按本机实测评分降序）。不满意可「回滚上一版」或「恢复初始默认」。</div>';
  // 自动应用：自定义项在前 + Top5，原子写入后热重载
  const merged = [...new Set([...custom, ...top.map(x => KP.normUpstream(x.upstream))])].join('\n');
  // 门禁②：先留回滚点；回滚点不可用就**不改写**（backupOnce 诚实返回，不再是永远 true）
  const okInitial = await DNS_STORE.ensureInitial();
  const okPrev = await DNS_STORE.refreshPrev();
  const gBackup = { ok: okInitial && okPrev, reason: '备份失败（没有回滚点，不改写配置）' };
  if (!KP.planGate(KP.DNS_OPTIMIZE_PLAN, 'backup-gate', gBackup).ok) {
    toast(`⚠️ ${gBackup.reason}`, 3200);
    return;
  }
  if (!await DNS_STORE.write('# 优选自动生成（自定义项在前）' + new Date().toLocaleString() + '\n' + merged)) {
    toast('❌ 配置写入失败，原配置未动（备份仍在）', 3600); return;
  }
  loadCurrentUpstreams();
  loadUpstreamEditor();
  });
}
async function rollback() {
  return withBusy($id('btn-rollback'), '回滚中…', async () => {
    const ok = await DNS_STORE.restore(DNS_PREV);
    if (ok) { toast('✅ 已回滚上一版'); refresh(); }
    else toast('没有可回滚的备份');
  });
}
// （setBind/setBindUI/restartDns 已删除：index.html 无绑定范围按钮，死代码按 deletion test 清除）
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
    ch.onclick = () => {
      const v = ch.dataset.v;
      const ta = document.getElementById('upstreams');
      // 切换语义（2026-10-02 用户报障）：绿（已加入）再点一次 = 从配置移除并变灰；
      // 灰再点 = 加入。仍要「保存」才落盘 —— 与手输添加是同一条编辑缓冲区。
      const r = KP.toggleUpstreamLine(ta.value, v);
      ta.value = r.text;
      renderCandChips();
      toast(r.removed ? '已从配置移除，记得「保存」' : '已加入配置，记得「保存」');
    };
  });
}
async function addUpstream() {
  const inp = document.getElementById('in-add');
  const v = KP.normUpstream(inp.value.trim());
  if (!v) { toast('请输入 DNS 地址'); return; }
  if (/127\.0\.0\.1|::1/.test(v)) { toast('❌ 禁止 127.0.0.1 / ::1'); return; }
  const ta = document.getElementById('upstreams');
  // 同一条整行判据（曾用子串，'223.5.5.5' 会被 doh URL 误判"已存在"）
  if (KP.hasUpstreamLine(ta.value, v)) { toast('已存在'); return; }
  ta.value = (ta.value.trim() ? ta.value.trim() + '\n' : '') + v;
  inp.value = '';
  renderCandChips();
  toast('已加入配置，记得「保存」');
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
