'use strict';
// srtrx 制御の Web 画面。フレームワークなし。サーバの JSON API(/api/…)だけを使う。

const $ = (sel, el = document) => el.querySelector(sel);

// 小さな DOM ヘルパー: h('div', {class:'a', onclick: fn}, 子...)
function h(tag, props, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (v === undefined || v === null || v === false) continue;
    if (k.startsWith('on') && typeof v === 'function') el.addEventListener(k.slice(2), v);
    else if (k === 'class') el.className = v;
    else if (k === 'text') el.textContent = v;
    else if (v === true) el.setAttribute(k, '');
    else el.setAttribute(k, v);
  }
  for (const kid of kids.flat()) {
    if (kid === undefined || kid === null || kid === false) continue;
    el.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
  }
  return el;
}

async function api(method, path, body) {
  const r = await fetch(path, {
    method, cache: 'no-store',
    headers: { 'Content-Type': 'application/json', 'X-SRTRX': '1' },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  let data = null;
  try { data = await r.json(); } catch (_) { /* 本文なし */ }
  if (!r.ok) {
    const e = new Error((data && data.error) || r.statusText);
    e.status = r.status; e.errors = (data && data.errors) || {};
    throw e;
  }
  return data;
}

// ---- 共通の部品 ----

let toastTimer;
function toast(msg, isErr) {
  const t = $('#toast');
  t.textContent = msg; t.className = 'toast' + (isErr ? ' err' : ''); t.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { t.hidden = true; }, isErr ? 6000 : 2500);
}

function modal(title, bodyNodes, actions) {
  $('#modalTitle').textContent = title;
  const body = $('#modalBody'); body.replaceChildren(...[].concat(bodyNodes));
  const acts = $('#modalActions'); acts.replaceChildren();
  const close = () => { $('#modal').hidden = true; };
  for (const a of actions) {
    acts.append(h('button', { class: 'btn' + (a.primary ? ' primary' : ''), onclick: () => { close(); if (a.fn) a.fn(); } }, a.label));
  }
  $('#modal').hidden = false;
  const first = acts.querySelector('button'); if (first) first.focus();
}
function confirmDialog(title, msg, okLabel, onOK) {
  modal(title, h('p', {}, msg), [{ label: 'キャンセル' }, { label: okLabel, primary: true, fn: onOK }]);
}
$('#modal').addEventListener('click', (e) => { if (e.target.id === 'modal') $('#modal').hidden = true; });
document.addEventListener('keydown', (e) => { if (e.key === 'Escape') $('#modal').hidden = true; });

const stateJP = { starting: '● 接続中', running: '● 動作中', backoff: '● 再起動待ち', stopped: '● 停止中' };
const fmtSec = (s) => (Math.max(0, s)).toFixed(1) + '秒';
const orDash = (s) => (s && String(s).trim()) ? s : '--';

function resetLabel(ms) {
  if (ms <= 0) return '自動リセットなし';
  if (ms % 3600000 === 0) return (ms / 3600000) + '時間ごとに自動リセット';
  if (ms % 60000 === 0) return (ms / 60000) + '分ごとに自動リセット';
  return (ms / 1000) + '秒ごとに自動リセット';
}

// ---- 状態ページ ----

const S = {};  // 状態ページの要素
function buildStatusPage() {
  const page = $('#page-status');
  const kv = (rows) => h('div', { class: 'kv' }, rows.flatMap(([label, id]) => [h('div', { class: 'k' }, label), h('div', { class: 'v', id })]));
  const card = (title, ...kids) => h('div', { class: 'card' }, h('div', { class: 'card-title' }, title), ...kids);
  page.replaceChildren(
    h('div', { class: 'row3' },
      card('MediaMTX(同梱)', h('span', { class: 'pill', id: 'mtxPill' }, '--'),
        kv([['経過', 'mtxSince'], ['再起動', 'mtxRestarts'], ['最後のエラー', 'mtxErr']])),
      card('ffmpeg', h('span', { class: 'pill', id: 'ffPill' }, '--'),
        kv([['経過', 'ffSince'], ['再起動', 'ffRestarts'], ['最後のエラー', 'ffErr'], ['デコード', 'ffDecode'], ['自己診断', 'ffDiag']])),
      card('プレイヤー', h('span', { class: 'pill', id: 'plPill' }, '--'),
        h('div', { class: 'big-num', id: 'plFps' }, '--'), h('div', { class: 'dim' }, '実測FPS'),
        kv([['プロセス', 'plPid'], ['最新フレーム', 'plAge']]))),
    h('div', { class: 'section-title', id: 'statTitle' }, '統計(リセットからの累計)'),
    h('div', { class: 'stats', id: 'stats' }),
    h('div', { class: 'banner', id: 'banner', hidden: true }),
    h('div', { class: 'actions' },
      h('button', { class: 'btn primary', onclick: () => act('start') }, '開始'),
      h('button', { class: 'btn', onclick: () => act('restart') }, '再起動'),
      h('button', { class: 'btn danger', onclick: () => act('stop') }, '停止'),
      h('span', { class: 'grow' }),
      h('button', { class: 'btn', onclick: () => act('reset') }, 'カウンターをリセット', h('kbd', {}, 'Ctrl+R'))));
  for (const id of ['mtxPill', 'mtxSince', 'mtxRestarts', 'mtxErr', 'ffPill', 'ffSince', 'ffRestarts', 'ffErr', 'ffDecode', 'ffDiag',
    'plPill', 'plFps', 'plPid', 'plAge', 'statTitle', 'stats', 'banner']) S[id] = $('#' + id, page);
}

async function act(action) {
  try { await api('POST', '/api/action', { action }); if (action === 'reset') toast('累計をリセットしました'); pollStatus(); }
  catch (e) { toast(e.message, true); }
}

function setPill(el, text, kind) { el.textContent = text; el.className = 'pill ' + kind; }

function renderStatus(st) {
  if (st.mediamtx_enabled) {
    setPill(S.mtxPill, stateJP[st.mediamtx.state] || st.mediamtx.state, st.mediamtx.state);
    S.mtxSince.textContent = fmtSec(st.mediamtx.since_sec) + (st.mediamtx.gate_wait ? '(前提の準備待ち)' : '');
    S.mtxRestarts.textContent = st.mediamtx.restart_count + ' 回';
    S.mtxErr.textContent = st.port_in_use ? 'ポート使用中(外部のMediaMTXが動いている可能性)' : orDash(st.mediamtx.last_error);
  } else {
    setPill(S.mtxPill, '● 外部を使用', 'stopped');
    S.mtxSince.textContent = S.mtxRestarts.textContent = '--';
    S.mtxErr.textContent = '同梱は使わない設定';
  }
  setPill(S.ffPill, stateJP[st.ffmpeg.state] || st.ffmpeg.state, st.ffmpeg.state);
  S.ffSince.textContent = fmtSec(st.ffmpeg.since_sec) + (st.ffmpeg.gate_wait ? '(前提の準備待ち)' : '');
  S.ffRestarts.textContent = st.ffmpeg.restart_count + ' 回';
  S.ffErr.textContent = orDash(st.ffmpeg.last_error);
  const hw = { none: 'ソフトウェア', vaapi: 'VAAPI', qsv: 'QSV', v4l2m2m: 'V4L2 M2M' }[st.hwaccel] || st.hwaccel;
  S.ffDecode.textContent = st.hw_fallback ? `ソフトウェア(${st.hwaccel} の失敗が続いたため自動で切替)` : hw;
  S.ffDiag.textContent = st.diag_error ? '失敗' : `合格(ffmpeg ${st.ffmpeg_version})`;

  if (st.player.connected) {
    setPill(S.plPill, '● 応答あり', 'running');
    S.plFps.textContent = st.player.fps.toFixed(1);
    S.plPid.textContent = 'pid ' + st.player.pid;
    S.plAge.textContent = st.player.frame_age_ms < 0 ? 'まだ無い' : st.player.frame_age_ms + ' ms 前';
  } else {
    setPill(S.plPill, '● 応答なし', 'error');
    S.plFps.textContent = S.plPid.textContent = S.plAge.textContent = '--';
  }

  S.statTitle.textContent = '統計(リセットからの累計 · ' + resetLabel(st.reset_interval_ms) + ')';
  S.stats.replaceChildren(...st.tiles.map((t) => h('div', { class: 'stat ' + t.color }, h('div', { class: 't' }, t.title), h('div', { class: 'n' }, t.value))));

  let text = '', kind = '';
  if (st.diag_error) { text = 'ffmpeg の自己診断に失敗: ' + st.diag_error + '(10秒ごとに再診断します)'; kind = 'error'; }
  else if (st.port_in_use) { text = 'ポート使用中: 外部のMediaMTXが動いている可能性があります。他のプロセスは止めません。'; kind = 'error'; }
  else if (!st.player.connected) { text = 'プレイヤー応答なし。systemd が再起動します(制御はプレイヤーを止めません)。'; kind = 'warn'; }
  else if (st.hw_fallback) { text = 'ハードウェアデコードの起動失敗が続いたため、ソフトウェアデコードで動いています。設定を適用すると戻ります。'; kind = 'warn'; }
  else if (st.load_notes.length) { text = '設定の読み込み: ' + st.load_notes.join(' / '); kind = 'ok'; }
  S.banner.hidden = !text; S.banner.textContent = text; S.banner.className = 'banner ' + kind;

  setLocked(st.locked);
}

// ---- 本番ロック ----

let locked = false;
function setLocked(v) {
  if (v === locked && $('#lockBtn').dataset.init) return;
  locked = v; $('#lockBtn').dataset.init = '1';
  $('#lockBtn').classList.toggle('on', v);
  $('#lockBtn').textContent = v ? '本番ロック中' : '本番ロック';
  if (settingsBuilt) { applyLockToForm(); }
}
$('#lockBtn').addEventListener('click', async () => {
  try {
    if (!locked) { await api('POST', '/api/lock', { on: true }); setLocked(true); toast('ロックしました'); }
    else confirmDialog('本番ロックを解除しますか?', '解除すると、設定を変更できます。展示中の誤操作にご注意ください。', '解除',
      async () => { try { await api('POST', '/api/lock', { on: false }); setLocked(false); toast('解除しました'); } catch (e) { toast(e.message, true); } });
  } catch (e) { toast(e.message, true); }
});

// ---- 設定ページ ----

let settingsBuilt = false;
let cfg = null;       // /api/config の応答(保存済みの値、スキーマなど)
let draft = {};       // 画面で編集中の値(キー → 値)
const rows = {};      // キー → {row, input, err, field, getter, setter, localErr}
let checkTimer = null;
const liveTimers = {};
let lastCheck = { ok: true, pending_restart: false };

function parseValue(f, text) {
  if (f.kind === 'int') { if (!/^-?\d+$/.test(text.trim())) throw new Error('整数で入力してください'); return parseInt(text, 10); }
  if (f.kind === 'float') { const n = Number(text); if (text.trim() === '' || Number.isNaN(n)) throw new Error('数値で入力してください'); return n; }
  return text;
}

function buildInput(f) {
  const key = f.key;
  if (f.kind === 'bool') {
    const input = h('input', { type: 'checkbox' });
    const el = h('label', { class: 'switch' }, input, h('span', { class: 'slider' }));
    input.addEventListener('change', () => edit(key, input.checked));
    return { el, input, get: () => input.checked, set: (v) => { input.checked = !!v; } };
  }
  if (f.kind === 'enum') {
    const input = h('select', {}, f.enum.map((o) => h('option', { value: o }, o)));
    input.addEventListener('change', () => edit(key, input.value));
    return { el: input, input, get: () => input.value, set: (v) => { input.value = v; } };
  }
  if (f.kind === 'tiles') {
    const boxes = {};
    const grid = h('div', { class: 'tilegrid' }, cfg.all_tiles.map((id) => {
      const cb = h('input', { type: 'checkbox' }); boxes[id] = cb;
      cb.addEventListener('change', () => edit(key, cfg.all_tiles.filter((t) => boxes[t].checked)));
      return h('label', {}, cb, cfg.tile_titles[id] || id);
    }));
    return { el: grid, input: grid, boxes, get: () => cfg.all_tiles.filter((t) => boxes[t].checked),
      set: (v) => { for (const t of cfg.all_tiles) boxes[t].checked = (v || []).includes(t); } };
  }
  const input = h('input', { type: 'text', autocomplete: 'off', spellcheck: 'false' });
  input.addEventListener('input', () => {
    try {
      const v = f.kind === 'args' ? input.value.trim().split(/\s+/).filter(Boolean) : parseValue(f, input.value);
      rows[key].localErr = null; edit(key, v);
    } catch (e) { rows[key].localErr = e.message; showError(key, e.message); scheduleCheck(); }
  });
  return { el: input, input,
    get: () => input.value,
    set: (v) => { input.value = f.kind === 'args' ? (v || []).join(' ') : String(v); } };
}

function showError(key, msg) {
  const r = rows[key]; if (!r) return;
  r.row.classList.toggle('has-error', !!msg);
  r.err.textContent = msg || '';
}

function edit(key, value) {
  draft[key] = value;
  const f = rows[key].field;
  showError(key, null);
  scheduleCheck();
  if (f.live && !locked) {
    clearTimeout(liveTimers[key]);
    liveTimers[key] = setTimeout(() => applyLive(key, value), 300);
  }
}

async function applyLive(key, value) {
  try { await api('POST', '/api/config/live', { key, value }); cfg.values[key] = value; scheduleCheck(); }
  catch (e) { showError(key, (e.errors && e.errors[key]) || e.message); if (e.status === 423) toast(e.message, true); }
}

function scheduleCheck() { clearTimeout(checkTimer); checkTimer = setTimeout(runCheck, 200); }

async function runCheck() {
  try {
    const r = await api('POST', '/api/config/check', { values: draft });
    lastCheck = r;
    for (const key of Object.keys(rows)) {
      const msg = rows[key].localErr || (r.errors && r.errors[key]) || null;
      // 自動で決まる欄は、検証の対象にしない
      showError(key, rows[key].derivedOn ? null : msg);
    }
    updateDerived(r.effective);
    updateApplyBar();
  } catch (e) { toast(e.message, true); }
}

function updateDerived(eff) {
  for (const key of Object.keys(rows)) {
    const r = rows[key], f = r.field;
    if (!f.derived) { r.derivedOn = false; continue; }
    r.derivedOn = !!draft[f.derived];
    r.input.disabled = r.derivedOn || locked;
    if (r.derivedOn && eff && key in eff) r.set(eff[key]);
  }
}

function updateApplyBar() {
  const hasLocal = Object.values(rows).some((r) => r.localErr);
  const bad = Object.entries(rows).some(([, r]) => r.row.classList.contains('has-error'));
  const valid = lastCheck.ok && !hasLocal && !bad;
  const btn = $('#applyBtn'), note = $('#applyNote');
  btn.disabled = !(valid && lastCheck.pending_restart && !locked);
  if (locked) { note.className = 'note warn'; note.textContent = '本番ロック中です(ヘッダーの「本番ロック」で解除)'; }
  else if (!valid) { note.className = 'note err'; note.textContent = '範囲外の値があります(赤い欄)。直すまで「適用」できません'; }
  else if (lastCheck.pending_restart) { note.className = 'note warn'; note.textContent = '未適用の変更があります。「適用して再起動」で反映します(映像が一瞬止まります)'; }
  else { note.className = 'note'; note.textContent = ''; }
}

function applyLockToForm() {
  $('#settingsForm').classList.toggle('locked-note', locked);
  for (const r of Object.values(rows)) r.input.disabled = locked || r.derivedOn;
  if (cfg) updateApplyBar();
}

async function loadConfig() {
  cfg = await api('GET', '/api/config');
  draft = JSON.parse(JSON.stringify(cfg.values));
  buildSettingsForm();
  for (const [k, v] of Object.entries(draft)) rows[k] && rows[k].set(v);
  lastCheck = { ok: true, pending_restart: false };
  updateDerived(cfg.effective);
  for (const k of Object.keys(rows)) showError(k, null);
  refreshProfiles();
  applyLockToForm();
  updateApplyBar();
}

let curSub = null;
function buildSettingsForm() {
  const page = $('#page-settings');
  for (const k of Object.keys(rows)) delete rows[k];
  const tabs = [...new Set(cfg.schema.map((f) => f.tab))];
  if (!curSub || !tabs.includes(curSub)) curSub = tabs[0];

  const panels = {};
  const subseg = h('nav', { class: 'seg' }, tabs.map((t) => h('button', { class: t === curSub ? 'on' : '', onclick: (e) => {
    curSub = t; for (const b of subseg.children) b.classList.toggle('on', b === e.currentTarget);
    for (const [name, p] of Object.entries(panels)) p.hidden = name !== t;
  } }, t)));
  for (const t of tabs) {
    const panel = h('div', { class: 'card', hidden: t !== curSub });
    for (const f of cfg.schema.filter((x) => x.tab === t)) {
      const ctl = buildInput(f);
      const err = h('div', { class: 'err' });
      const row = h('div', { class: 'field' },
        h('label', { title: f.key }, f.label), ctl.el, h('span', { class: 'tag ' + (f.live ? 'live' : 'restart') }, f.live ? '即時' : '再起動'), err);
      rows[f.key] = { row, input: ctl.input, err, field: f, get: ctl.get, set: ctl.set, localErr: null, derivedOn: false };
      panel.append(row);
    }
    panels[t] = panel;
  }

  const profSel = h('select', { id: 'profSel' });
  const profName = h('input', { type: 'text', id: 'profName', placeholder: '保存名(例: 本番)' });
  const profile = h('div', { class: 'card' },
    h('div', { class: 'card-title' }, 'プロファイル'),
    h('div', { class: 'profile-row' },
      h('div', { style: 'width:180px' }, profSel),
      h('button', { class: 'btn', onclick: switchProfile }, '切り替え'),
      h('span', { class: 'grow' }),
      h('div', { style: 'width:180px' }, profName),
      h('button', { class: 'btn', onclick: saveProfile }, '現在の設定を保存')));

  const form = h('div', { id: 'settingsForm', class: 'page' }, profile, subseg, ...Object.values(panels));
  page.replaceChildren(form,
    h('div', { class: 'applybar' }, h('div', { class: 'note', id: 'applyNote' }),
      h('button', { class: 'btn primary', id: 'applyBtn', onclick: applyRestart }, '適用して再起動')));
  settingsBuilt = true;
}

function refreshProfiles() {
  const sel = $('#profSel'); if (!sel) return;
  sel.replaceChildren(...(cfg.profiles.length ? cfg.profiles : ['(なし)']).map((p) => h('option', { value: p }, p)));
}

async function applyRestart() {
  confirmDialog('設定を適用して再起動しますか?',
    'ffmpeg(と、MediaMTXの設定が変わったときはMediaMTX)を再起動します。映像が一瞬止まります。最後のフレームは保持されます。',
    '適用して再起動', async () => {
      try { await api('POST', '/api/config/apply', { values: draft }); toast('適用しました'); await loadConfig(); }
      catch (e) { for (const [k, m] of Object.entries(e.errors || {})) showError(k, m); toast('適用できない: ' + e.message, true); }
    });
}

async function saveProfile() {
  const name = $('#profName').value.trim();
  if (!name) { toast('保存名を入れてください', true); return; }
  try { await api('POST', '/api/profiles/save', { name }); toast(`プロファイル「${name}」を保存しました(適用済みの設定)`); await loadConfig(); }
  catch (e) { toast(e.message, true); }
}

async function switchProfile() {
  const name = $('#profSel').value;
  if (!name || name === '(なし)') return;
  try {
    const r = await api('POST', '/api/profiles/load', { name });
    confirmDialog(`プロファイル「${name}」へ切り替えますか?`, '設定を適用して、ffmpegを再起動します。', '切り替える', async () => {
      try { await api('POST', '/api/config/apply', { values: r.values }); toast('切り替えました'); await loadConfig(); }
      catch (e) { toast('切り替えできない: ' + e.message, true); }
    });
  } catch (e) { toast(e.message, true); }
}

// ---- ログページ ----

function buildLogsPage() {
  $('#page-logs').replaceChildren(
    h('div', { class: 'card' }, h('div', { class: 'card-title' }, '障害履歴(直近100件、新しい順)'), h('div', { class: 'log', id: 'faultLog' })),
    h('div', { class: 'card' }, h('div', { class: 'card-title' }, 'ログ(ffmpegのstderrを含む)'), h('div', { class: 'log tall', id: 'lineLog' })));
}

async function refreshLogs() {
  const r = await api('GET', '/api/logs');
  const fl = $('#faultLog');
  fl.replaceChildren(...(r.faults.length ? r.faults.map((f) => h('div', { class: 'fault' },
    h('span', { class: 't' }, f.time + ' '), h('span', { class: 'who' }, `[${f.target}] `), h('span', { class: 'why' }, f.reason),
    f.stderr.length ? h('span', { class: 'se' }, 'stderr: ' + f.stderr.join(' | ')) : null))
    : [h('span', { class: 'ok-text' }, '障害はありません')]));
  const ll = $('#lineLog');
  const atBottom = ll.scrollHeight - ll.scrollTop - ll.clientHeight < 40;
  ll.replaceChildren(...r.lines.map((l) => h('div', { class: l.level }, h('span', { class: 't' }, l.time + ' '), `${l.level.padEnd(5)} ${l.msg} ${l.attrs}`)));
  if (atBottom) ll.scrollTop = ll.scrollHeight;
}

// ---- ページ切り替えとポーリング ----

let tab = 'status';
function showTab(name) {
  tab = name;
  for (const b of document.querySelectorAll('#tabs button')) b.classList.toggle('on', b.dataset.tab === name);
  for (const n of ['status', 'settings', 'logs']) $('#page-' + n).hidden = n !== name;
  if (name === 'settings') loadConfig().catch((e) => toast(e.message, true));
  if (name === 'logs') refreshLogs().catch(() => {});
}
for (const b of document.querySelectorAll('#tabs button')) b.addEventListener('click', () => showTab(b.dataset.tab));

async function pollStatus() {
  try { renderStatus(await api('GET', '/api/status')); $('#page-status').classList.remove('stale'); }
  catch (e) { $('#page-status').classList.add('stale'); }
}
setInterval(pollStatus, 1000);
setInterval(() => { if (tab === 'logs') refreshLogs().catch(() => {}); }, 2000);

$('#infoBtn').addEventListener('click', async () => {
  try {
    const r = await api('GET', '/api/info');
    modal('情報', [h('p', {}, `同梱: MediaMTX v${r.mediamtx_version}(MITライセンス)`), h('pre', {}, r.license)], [{ label: '閉じる', primary: true }]);
  } catch (e) { toast(e.message, true); }
});

document.addEventListener('keydown', (e) => {
  if ((e.ctrlKey || e.metaKey) && (e.key === 'r' || e.key === 'R') && tab === 'status') { e.preventDefault(); act('reset'); }
});

buildStatusPage();
buildLogsPage();
pollStatus();
