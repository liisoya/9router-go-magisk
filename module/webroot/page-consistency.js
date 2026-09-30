// @ts-check
/* page-consistency.js — 一致性检查页：孤儿数据扫描与清理 / 无凭据活跃连接扫描
 *
 * 顺序不变量来自 KP.ORPHAN_CLEAN_PLAN（本文件只按求值结果执行，不自己判断顺序）。
 * 出厂 key 的检查/补入 UI 已移除（用户无感）：新装由 service.sh 开机自动补入；导入场景
 * 由引擎会话鉴权兜底（Phase 10）；用户主动删除时不复活 —— 安全特性，外部 CLI 需要时
 * 在 Dashboard 的 keys 页手动添加。
 * 2026-09-28 从 app.js 纯搬迁（零行为变化）。
 */

async function scanOrphans() {
  return withBusy($id('btn-scan'), '扫描中…', async () => {
    const box = $id('orphan-list');
    const btn = $id('btn-clean-orphans');
    btn.disabled = true; btn.style.display = 'none';
    box.innerHTML = '<div class="hint">扫描中…</div>';
    const r = await KB.sqlFile(KB._cmds.scanOrphansSql());
    const { live, aliases } = KP.parseScanLines(r.out.split('\n'));
    // 安全护栏：有模型别名却读不到任何节点/连接 = 扫描结果不可信（撞上引擎写事务等），
    // 绝不在这种状态下判定孤儿 —— 宁可扫不出，不可误删。
    // 只求值 scan 这一道门禁（planGate 阶段切片）：planSteps 的"缺 fact = 拒绝"语义下
    // 传整计划必然被后续门禁假拦（2026-09-28 回归，孤儿清理整体失联）。
    // r.ok=false = sqlFile 三次重试后仍读失败（空输出）→ 同样不可信，不得显示"未发现"
    const scan = { ok: r.ok === true && !(aliases.size > 0 && live.size === 0),
                   reason: '扫描结果异常（未读到任何节点/连接，可能正被引擎写入占用）' };
    if (!KP.planGate(KP.ORPHAN_CLEAN_PLAN, 'scan', scan).ok) {
      box.innerHTML = `<div class="hint">⚠️ ${esc(scan.reason)}，已中止判定。请稍后重试。</div>`;
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
    const confirm = await KB.sqlFile(KB._cmds.recheckOrphansSql(state.orphans));
    // 复查读失败 = 结果不可信，与扫描门禁同一判据纪律（不得把空输出当"查无存活"放行删除）
    const recheck = { ok: confirm.ok === true, reason: '复查读取失败（结果不可信）' };
    if (!KP.planGate(KP.ORPHAN_CLEAN_PLAN, 'recheck', recheck).ok) {
      toast('⚠️ 复查读取失败，已中止删除（安全优先）', 3200); return;
    }
    const stillLive = confirm.out.split('\n').map(s => s.trim()).filter(Boolean);
    if (stillLive.length) {
      state.orphans = state.orphans.filter(a => !stillLive.includes(a));
      toast(`⚠️ ${stillLive.length} 项复查后确认仍存活，已从清理列表剔除`, 3600);
      // 必须 await（2026-09-29 诊断）：不 await 会让这次扫描在下面的 await 之后回写
      // state.orphans → 快照取旧数组、删除取新数组（同一批 alias 没一起走完门禁与动作）。
      await scanOrphans();
      if (!state.orphans.length) return;
    }
    // 绑定本批目标：快照与删除**必须针对同一集合**，这是 ORPHAN_CLEAN_PLAN 的核心不变量
    // （快照在删除之前，且覆盖同一批 alias）。用局部变量固定下来，后续 state 再变也不影响本批。
    const targets = state.orphans.slice();
    const n = targets.length;
    // 删除前快照：把将被删除的行以 INSERT 语句形式存档，任何误删都可精确回滚。
    // SQL 来自 _cmds.orphanSnapshotSql（唯一所有者，离线断言"不翻倍引号"——
    // 2026-09-28 事故：装配层内联 SQL 把单引号翻倍 → sqlite3 Parse error → 快照永远失败）。
    const ts = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19);
    const snapOk = await KB.sqlSnapshot(
      KB._cmds.orphanSnapshotSql(targets),
      `${CFG.DATA_DIR}/backups/kv-before-orphan-clean-${ts}.sql`);
    // 快照门禁在 delete 之前（顺序不变量见 KP.ORPHAN_CLEAN_PLAN，离线有断言）
    if (!KP.planGate(KP.ORPHAN_CLEAN_PLAN, 'snapshot', { ok: snapOk, reason: '快照失败' }).ok) {
      toast('⚠️ 快照失败，已中止删除（安全优先）', 3200); return;
    }
    // 单语句 OR 链式删除：一次点击全清（不逐条）
    // **必须用本批冻结的 targets，不是 state.orphans**（2026-09-30 架构扫描发现：
    // I5 修复只让快照用了 targets，删除漏了）。「检查孤儿数据」按钮在清理期间没有被禁用，
    // 用户在快照的 await 窗口里点它就会改写 state.orphans → DELETE 换成另一批别名：
    // 本批漏删、另一批没有快照可回滚，界面却照样报"已一次性清理 N 项"（谎报成功）；
    // 批次变空时 DELETE 甚至退化成 `... AND ()` 语法错，仍然报成功。
    if (!n) return; // 本批为空：不发 SQL（空 IN 列表是语法错，且无事可做）
    await KB.sqlFile(KB._cmds.orphanDeleteSql(targets));
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
    const conns = await KB.sqlFile(KB._cmds.credScanSql());
    // 读失败不得冒充"凭据齐全"（空输出的假阴性比没有结果更危险）
    if (conns.ok === false) {
      box.innerHTML = '<div class="hint">⚠️ 数据库读取失败（引擎可能正忙），请稍后重试。</div>';
      return;
    }
    const rows = KP.parseCredScan(conns.out);
    box.innerHTML = rows.length
      ? rows.map(r => `<div class="list-item"><span class="tag err">${esc(r.authType)}</span><span style="flex:1">${esc(r.provider)}</span></div>`).join('')
      : '<div class="hint">✅ 活跃连接凭据齐全</div>';
  });
}
$id('btn-scan').onclick = scanOrphans;
$id('btn-clean-orphans').onclick = cleanOrphans;
$id('btn-scan-conn').onclick = scanCred;
