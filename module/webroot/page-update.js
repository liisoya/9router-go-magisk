// @ts-check
/* page-update.js — 更新页：GitHub 加速测速与选择 / 引擎更新 / 模块更新 / 更新源
 *
 * 上游地址一律经 KU（upstream.js 是唯一所有者）—— 本文件不许出现任何地址字面量，
 * upstream.test.js 会拿 index.html 清单里的**全部**面板脚本做回潮扫描。
 * 2026-09-28 从 app.js 纯搬迁（零行为变化）。
 */

const ACCEL_SEL = CFG.DATA_DIR + '/github-accel';
const ACCEL_LIST = CFG.DATA_DIR + '/accel-list.conf';
const MOD_UPDATE_URL_FILE = CFG.DATA_DIR + '/module-update-url';
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
      box.innerHTML = `<div class="hint">测速中… 已完成 ${start}/${all.length}（每批 ${BATCH} 个并发，已用 ${elapsed()}s）</div>`;
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
      `<div class="list-item"><span class="tag">${i + 1}</span><span style="flex:1">${esc(x.node)}</span><span class="tag">${x.ms.toFixed(0)} ms</span><button data-u="${escAttr(x.node)}" class="pick2">选</button></div>`).join('')
      : '<div class="hint">所有节点都不可达，可直连或添加自定义节点</div>')
      + (failures.length ? `<div class="hint">不可用 ${failures.length} 个：${failures.map(f => esc(f.node) + '（' + esc(f.why) + '）').join('；')}</div>` : '')
      + `<div class="hint">用时 ${elapsed()}s${applied ? `，已自动选中最快：${esc(applied)}` : ''}${cur && applied && applied !== cur ? `（原选中 ${esc(cur)}）` : ''}</div>`;
    box.querySelectorAll('.pick2').forEach(b => {
      b.onclick = async () => {
        if (!await KB.writeFile(ACCEL_SEL, b.dataset.u + '\n')) { toast('❌ 写入失败'); return; }
        toast('已选中：' + esc(b.dataset.u)); renderAccelCur();
      };
    });
    if (applied) renderAccelCur(applied);
  });
}
async function addAccel() {
  const u = prompt('自定义加速节点前缀（以 / 结尾，GitHub URL 会拼在后面）：');
  if (!u) return;
  if (!await KB.appendLine(ACCEL_LIST, u)) { toast('❌ 添加失败'); return; } // 完整保留 URL（曾 strip 单引号破坏含引号 URL）
  // 不重渲染「当前加速」：往候选**列表**里加一项，当前**选中**并没有变；
  // 而无参 renderAccelCur() 会去读文件，选中文件不存在（= 直连 GitHub 的正常态）时
  // 会被显示成「未知（读取失败）」（2026-09-29 诊断，与 toast 互相打脸）。
  toast('已添加');
}
async function clearAccel() {
  await KB.remove(ACCEL_SEL);
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
  if (r.out.trim() !== 'engine=up') { out.textContent += `\n❌ 安装/重启失败：${r.out.trim()}`; return; }
  await refresh();
  toast('✅ 引擎更新完成', 3200); out.textContent += '\n✅ 完成';
}
async function modCheck() {
  return withBusy($id('btn-mod-check'), '检查中…', async () => {
    const out = $id('mod-out');
    out.style.display = 'block'; out.textContent = '检查中…';
    const url = state.modUrl || KU.DEFAULT_MOD_UPDATE_URL;
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
  if (r.out.trim() !== 'engine=up') { out.textContent += `\n❌ 安装/重启失败：${r.out.trim()}`; return; }
  await refresh();
  toast('✅ 模块更新完成', 3200); out.textContent += '\n✅ 完成';
}
async function setModUrl() {
  const u = prompt('模块更新源 URL（指向 update.json）：', state.modUrl || KU.DEFAULT_MOD_UPDATE_URL);
  if (!u) return;
  if (!await KB.writeFile(MOD_UPDATE_URL_FILE, u + '\n')) { toast('❌ 保存失败'); return; }
  state.modUrl = u; toast('已保存');
}
$id('btn-eng-check').onclick = engCheck;
$id('btn-eng-update').onclick = engUpdate;
$id('btn-mod-check').onclick = modCheck;
$id('btn-mod-update').onclick = modUpdate;
$id('btn-mod-seturl').onclick = setModUrl;
