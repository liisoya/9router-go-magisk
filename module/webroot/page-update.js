// @ts-check
/* page-update.js — 更新页：GitHub 加速测速与选择 / 引擎更新 / 模块更新 / 更新源
 *
 * 上游地址一律经 KU（upstream.js 是唯一所有者）—— 本文件不许出现任何地址字面量，
 * upstream.test.js 会拿 index.html 清单里的**全部**面板脚本做回潮扫描。
 * 2026-09-28 从 app.js 纯搬迁（零行为变化）。
 */

const ACCEL_SEL = CFG.DATA_DIR + '/github-accel';
const ACCEL_LIST = CFG.DATA_DIR + '/accel-list.conf';
// 这里**没有**"可写的更新源文件"了（2026-10-01）：更新源固定为 KU.MOD_UPDATE_URL，
// 面板不给输入、也不落盘 —— 可写的更新源 = 可指向任意 zip。旧的那个数据文件名刻意
// 连注释里都不再出现：留着它，就是把"还能这样改"这条知识又传下去。
// 两条更新通道的地址（清单 URL / release 资产 URL / 加速前缀拼接）一律经 KUpstream。
// 这里曾手写 release URL 且直接用 version.json 的裸版本号当 tag → 每次「下载并更新引擎」
// 都 404（2026-09-27 用户实测）。地址契约不再由装配层持有。
// 初始加速清单来源：moretools.app/github-proxy 聚合（2026-09-25 测速可用前 15）
const BUILTIN_ACCEL = [
  'https://github.cnxiaobai.com/', 'https://gitproxy.mrhjx.cn/', 'https://github.chenc.dev/',
  'https://ghp.keleyaa.com/', 'https://ghproxy.xzhouqd.com/', 'https://github-proxy.memory-echoes.cn/',
  'https://gh.ddlc.top/', 'https://gh.dpik.top/', 'https://ghproxy.cxkpro.top/',
  'https://gh.padao.fun/', 'https://hub.ddayh.com/', 'https://gh.xxooo.cf/',
  'https://ghfile.geekertao.top/', 'https://github-proxy.lixxing.top/', 'https://gh-proxy.com/'
];
// ═══════════ 更新 ═══════════
// 读"当前选中的加速节点"的**唯一入口**（2026-09-30 架构扫描 C4）。
// 为什么要有它：这段知识（从哪个文件读；空串 = 直连；**读失败 r.ok===false 不等于"没选"**）
// 原先在 5 个调用点各写一遍，I6 正是漏掉最后一条 —— 清掉选中后无参重渲染，把正常态渲染成
// 「未知（读取失败）」。调用方现在只问"选中的是谁"，不再各自解释 ok 与空串。
// 返回判别式结果：ok=false 只能来自**读失败**（与"没选"严格区分）。
async function readAccelSel() {
  const r = await KB.readFile(ACCEL_SEL);
  if (r.ok === false) return { ok: false, sel: '' };
  return { ok: true, sel: r.out.trim() };
}
async function renderAccelCur(sel) {
  if (sel === undefined) {
    const r = await readAccelSel();
    // 读失败不得渲染成"直连 GitHub"（那是把故障说成配置）
    if (!r.ok) { document.getElementById('accel-cur').textContent = '未知（读取失败）'; return; }
    sel = r.sel;
  }
  document.getElementById('accel-cur').textContent = sel || '直连 GitHub';
  return sel;
}
async function speedTest() {
  return withBusy($id('btn-speed'), '测速中…', async () => {
    const custom = (await KB.readFile(ACCEL_LIST)).out.split('\n').map(s => s.trim()).filter(Boolean);
    const all = [...new Set([...BUILTIN_ACCEL, ...custom])];
    const target = KU.ENGINE_VERSION_FILE_URL;
    const results = [], failures = [];
    const box = document.getElementById('accel-list');
    const cur = (await readAccelSel()).sel;   // 这里只要"当前值"用于比较与文案，渲染状态在 renderAccelCur
    const t0 = Date.now();
    const elapsed = () => Math.round((Date.now() - t0) / 1000);
    // 分批并发（A5）：并发放在 **shell 内部**（见 bridge.curlTimingBatch），批间更新进度。
    // 每批 8 个（与 dnsfwd 的 -j 8 同量级）；单节点连接超时 3s —— **失败节点才是耗时大头**
    // （真机实测：挂死的代理 5.03s → 3.00s；可用节点 0.7s 就回来）。
    const BATCH = 8;
    for (let start = 0; start < all.length; start += BATCH) {
      const chunk = all.slice(start, start + BATCH);
      box.innerHTML = `<div class="hint">测速中… ${start}/${all.length}（已用 ${elapsed()}s）</div>`;
      const r = await KB.curlTimingBatch(chunk.map(n => n + target), 'b' + start);
      for (const row of KP.parseCurlTimings(r.out)) {
        const node = chunk[row.i];
        if (!node) continue;
        if (row.ok) results.push({ node, ms: row.ms });
        else failures.push({ node, why: row.err || '连不上或超时' });
      }
    }
    results.sort((a, b) => a.ms - b.ms);
    const top = results.slice(0, 5);
    // 测速即选中（A5）：与 DNS 优选的"测速后自动应用"同一惯例 —— 省掉"测完还得手动点一个"
    // 这一步（那正是"得先选节点才能用"的摩擦来源）。与已选相同就不写设备（省一次 root shell、
    // 也少一次闪存写）。想换：点别的节点 /「清除选中」。
    const fastest = top.length ? top[0].node : '';
    let applied = fastest;
    if (fastest && fastest !== cur) {
      applied = (await KB.writeFile(ACCEL_SEL, fastest + '\n')) ? fastest : '';
      if (!applied) toast('❌ 选中写入失败，仍用原选中项', 3000);
    }
    box.innerHTML = (top.length ? top.map((x, i) =>
      `<div class="list-item"><span class="tag">${i + 1}</span><span class="grow">${esc(x.node)}</span><span class="tag">${x.ms.toFixed(0)} ms</span><button data-u="${escAttr(x.node)}" class="pick2">选</button></div>`).join('')
      : '<div class="hint">所有节点都不可达，可直连或添加自定义节点</div>')
      // 失败明细收进 title：把十个节点连同原因铺成一行是"很长很冗余"的典型
      // （2026-09-30 用户反馈）；计数留在台面，细节悬停可查。
      + (failures.length ? `<div class="hint" title="${escAttr(failures.map(f => f.node + '（' + f.why + '）').join('；'))}">不可用 ${failures.length} 个</div>` : '')
      + `<div class="hint">用时 ${elapsed()}s${applied ? ` · 已自动选中最快：${esc(applied)}` : ''}</div>`;
    box.querySelectorAll('.pick2').forEach(b => {
      b.onclick = async () => {
        if (!await KB.writeFile(ACCEL_SEL, b.dataset.u + '\n')) { toast('❌ 写入失败'); return; }
        toast('已选中：' + esc(b.dataset.u)); renderAccelCur();
      };
    });
    if (applied) renderAccelCur(applied);
  });
}
// 2026-10-01：原先用原生 prompt()。诊断时发现它在无头浏览器里会把页面挂死；
// 更实际的风险是**某些 WebView 会禁用 JS 对话框** —— 那样按钮点了完全没反应、也不报错。
// 改成与「修改端口 / 直接编辑配置文件」同一套模式：读数在前、编辑折起来 + 内联输入框。
async function addAccel() {
  const inp = $id('in-accel');
  const u = (inp.value || '').trim();
  if (!u) { toast('请输入节点前缀（以 / 结尾）'); inp.focus(); return; }
  if (!await KB.appendLine(ACCEL_LIST, u)) { toast('❌ 添加失败'); return; } // 完整保留 URL（曾 strip 单引号破坏含引号 URL）
  inp.value = '';
  // 不重渲染「当前加速」：往候选**列表**里加一项，当前**选中**并没有变；
  // 而无参 renderAccelCur() 会去读文件，选中文件不存在（= 直连 GitHub 的正常态）时
  // 会被显示成「未知（读取失败）」（2026-09-29 诊断，与 toast 互相打脸）。
  toast('已添加');
}
async function clearAccel() {
  // remove 已自报成败（rm-ok）：失败要如实报，不能假装清掉了
  if (!await KB.remove(ACCEL_SEL)) { toast('❌ 清除失败，选中保持不变', 3600); return; }
  // 已知结果就直说：清掉选中后就是"直连 GitHub"。不要再走"读文件"那条路去猜 ——
  // 文件不存在是这里的**正常结果**，读不到与读失败在 readFile 里是同一个 ok:false。
  toast('已清除选中，直连 GitHub');
  renderAccelCur('');
}
document.getElementById('btn-speed').onclick = speedTest;
document.getElementById('btn-accel-add').onclick = addAccel;
document.getElementById('btn-accel-clear').onclick = clearAccel;

async function engCheck() {
  return withBusy($id('btn-eng-check'), '检查中…', async () => {
    const out = $id('eng-out');
    out.style.display = 'block'; out.textContent = '检查中…';
    const p = (await readAccelSel()).sel;   // 只用于给 GitHub 地址拼加速前缀（读失败=按直连处理，与旧行为一致）
    const r = await KB.fetch(KU.withAccel(KU.ENGINE_VERSION_URL, p), 15);
    let latest = '';
    try { latest = JSON.parse(r.out).latestVersion || ''; } catch {}
    if (!latest) { out.textContent = '❌ 无法获取上游版本（可先测速选择加速节点）'; return; }
    document.getElementById('eng-latest').textContent = latest;
    // 引擎当前版本用真实来源（panel 的 engine_version），绝不拿模块版本冒充
    const cur = snapshot.engineVersion;
    if (!cur) { out.textContent = `上游 ${latest}\n⚠️ 本地引擎版本未知（旧版模块安装，重装/更新模块后可显示）——是否更新请自行判断`; return; }
    out.textContent = `当前 ${cur} / 上游 ${latest}\n` + (KP.cmpVer(cur, latest) > 0 ? '有更新可用' : '已是最新');
    document.getElementById('btn-eng-update').disabled = KP.cmpVer(cur, latest) <= 0;
    state.engLatest = latest;
  });
}
async function engUpdate() {
  const ver = state.engLatest; if (!ver) return;
  const out = document.getElementById('eng-out');
  const p = (await readAccelSel()).sel;   // 只用于给 GitHub 地址拼加速前缀（读失败=按直连处理，与旧行为一致）
  // 地址由 KUpstream 出（tag 形态的唯一所有者），并把**最终下载地址**显示出来：
  // 这次定位最痛的一点就是面板只说"下载失败"、不说"下的是哪个地址"（2026-09-27）
  const dlUrl = KU.withAccel(KU.engineAssetUrl(ver), p);
  const sumsUrl = KU.withAccel(KU.engineSumsUrl(ver), p);
  out.textContent = (p ? `加速 ${p}\n` : '直连 GitHub\n') + `下载地址 ${dlUrl}\n下载中…`;
  if (!await KB.download(dlUrl, '/data/local/tmp/9r-eng.new', 300)) {
    out.textContent = `❌ 下载失败（HTTP 非 2xx 或网络中断）—— 设备上的引擎未改动\n下载地址 ${dlUrl}`;
    return;
  }
  // 装前门禁①：像不像一个引擎（体积 + ELF 魔数）。不合格绝不进 install-engine。
  // 2026-09-26 事故：加速节点返回 404 正文 "Not Found"（9 字节），被当成引擎装上。
  const size = await KB.fileSize('/data/local/tmp/9r-eng.new');
  const magic = await KB.elfMagic('/data/local/tmp/9r-eng.new');
  const gFile = KP.engineFileGate(size, magic);
  // 只求值本阶段门禁（planGate）：planSteps 整计划求值时 sum-gate 无 fact → 假拦，
  // 引擎更新在校验和之前必然中止（2026-09-28 走查发现的活体 bug）
  if (!KP.planGate(KP.ENGINE_UPDATE_PLAN, 'file-gate', gFile).ok) {
    out.textContent += `\n❌ ${gFile.reason}\n已中止，设备上的引擎未改动`; return;
  }
  // 装前门禁②：校验和 —— **取不到就拒绝**（旧实现"取不到跳过校验"正好放行了 404 正文）
  out.textContent += '\n校验 SHA256…';
  // KB.fetch 带 -L：release 资产地址是 302 跳转，不跟随就只剩空正文（真机实测 302 size=0）
  const sum = await KB.fetch(sumsUrl, 60);
  const expected = KU.parseSumFor(sum.out, KU.ENGINE_ASSET);
  const actual = await KB.sha256('/data/local/tmp/9r-eng.new');
  const gSum = KP.checksumGate(expected, actual);
  // 两个门禁都过才允许 install —— 由计划求值决定（顺序不变量在 KP.ENGINE_UPDATE_PLAN，
  // 离线断言"任一不过则 install 不可达"；这里不再手写顺序判断）
  if (!KP.planGate(KP.ENGINE_UPDATE_PLAN, 'sum-gate', gSum).ok) {
    out.textContent += `\n❌ ${gSum.reason}\n校验和地址 ${sumsUrl}`; return;
  }
  out.textContent += ' ✅\n替换二进制并重启…';
  // 安装唯一入口：门禁 → 回滚点 → 替换 → 权限 → 起来后才写版本，全在 ops.sh seam 内
  const r = await KB.ops(`install-engine /data/local/tmp/9r-eng.new ${ver}`);
  // 成功词集走 KP.ACTION_WORDS（与 runOpsAction 同一判定，不许再有第二个家）
  if (!KP.actionOk('install-engine', r.out)) { out.textContent += `\n❌ 安装/重启失败：${r.out.trim()}`; return; }
  await refresh();
  toast('✅ 引擎更新完成', 3200); out.textContent += '\n✅ 完成';
}
async function modCheck() {
  return withBusy($id('btn-mod-check'), '检查中…', async () => {
    const out = $id('mod-out');
    out.style.display = 'block'; out.textContent = '检查中…';
    // 更新源是**固定**的（唯一所有者 KUpstream）：不读快照、不读用户文件 ——
    // 老设备上残留的自定义值正是要防的东西（理由见 KU.MOD_UPDATE_URL 的注释）。
    const url = KU.MOD_UPDATE_URL;
    const p = (await readAccelSel()).sel;   // 只用于给 GitHub 地址拼加速前缀（读失败=按直连处理，与旧行为一致）
    const target = KU.withAccelIfGithub(url, p);
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
  const p = (await readAccelSel()).sel;   // 只用于给 GitHub 地址拼加速前缀（读失败=按直连处理，与旧行为一致）
  const dlUrl = KU.withAccelIfGithub(j.zipUrl, p);
  out.textContent = '下载模块 zip…';
  if (!await KB.download(dlUrl, '/data/local/tmp/mod-update.zip', 600)) { out.textContent = '❌ 下载失败'; return; }
  const chk = await KB.zipList('/data/local/tmp/mod-update.zip');
  // zip 门禁必须在 install-module 之前（顺序不变量见 KP.MODULE_UPDATE_PLAN，离线有断言）
  const gZip = { ok: chk.out.includes('module.prop'), reason: 'zip 内容异常（缺 module.prop）' };
  if (!KP.planGate(KP.MODULE_UPDATE_PLAN, 'zip-gate', gZip).ok) {
    out.textContent = `❌ ${gZip.reason}，已放弃`; return;
  }
  out.textContent += '\n停进程 → 解压覆盖 → 重启…';
  // 安装唯一入口：备份 → 解压 → chmod 兜底（含 lib/）→ 清理 → 重启，全在 ops.sh seam 内
  const r = await KB.ops('install-module /data/local/tmp/mod-update.zip');
  if (!KP.actionOk('install-module', r.out)) { out.textContent += `\n❌ 安装/重启失败：${r.out.trim()}`; return; }
  await refresh();
  toast('✅ 模块更新完成', 3200); out.textContent += '\n✅ 完成';
}
$id('btn-eng-check').onclick = engCheck;
$id('btn-eng-update').onclick = engUpdate;
$id('btn-mod-check').onclick = modCheck;
$id('btn-mod-update').onclick = modUpdate;

/** 更新页的快照应用器（自注册，refresh 广播时调用） */
function applyUpdate() {
  // 项目地址：href / 文案 / title 全从 KU 现取 —— HTML 里只留一个空链接占位，
  // 地址字面量出现在 upstream.js 之外会被回潮扫描判红（地址契约只有一个所有者）。
  // 文案用短形式（仓库名），完整地址放 title：一行里塞完整 URL 在手机上必换行。
  // cast 到 HTMLAnchorElement：$id 返回的是 HTMLElement（没有 href），链接是锚点。
  const a = /** @type {HTMLAnchorElement} */ ($id('mod-project'));
  if (a) {
    a.href = KU.PROJECT_URL;
    a.textContent = KU.MODULE_REPO;
    a.title = KU.PROJECT_URL;
    // 点击走 root shell 的 am start 唤起系统浏览器：WebView 宿主不一定处理
    // target="_blank"（真机实测点项目地址没反应），shell 这条路不赌宿主实现。
    // href 与默认跳转保留：桌面浏览器里照常跳，长按复制也仍可用。
    a.onclick = async (e) => {
      if (e && e.preventDefault) e.preventDefault();
      const r = await KB.openUrl(KU.PROJECT_URL);
      if (r && /Starting:/.test(r.out)) toast('✅ 已在浏览器打开项目主页');
      else toast('⚠️ 无法自动打开，请在浏览器访问 ' + KU.PROJECT_URL, 4200);
    };
  }
}
onPanelSnapshot(applyUpdate);
applyUpdate();   // 首屏就填上（不等第一份快照到达；幂等）
