// 界面逻辑：只负责渲染与调用本地 API，不做任何磁盘操作。
const TOKEN = new URLSearchParams(location.search).get('t') || '';

// 支持 ?t=xxx#trash 直达某个标签，方便排查问题和收藏
const TABS = ['all', 'archived', 'orphan', 'trash'];
const HASH_TAB = location.hash.replace('#', '');

const state = {
  data: null,
  tab: TABS.includes(HASH_TAB) ? HASH_TAB : 'all',
  query: '',
  selected: new Set(),
};

const $ = (sel) => document.querySelector(sel);

/* ---------------- 工具函数 ---------------- */

// 写操作串行：连点两次删除会并发读索引再各自写回，界面上也会弹两次确认。
// 后端有互斥兜底，这里先从交互上挡掉。
let writing = false;

async function api(path, payload) {
  const isWrite = payload !== undefined;
  if (isWrite) {
    if (writing) throw new Error('上一个操作还没完成，请稍等一秒再试');
    writing = true;
  }
  try {
    const res = await fetch(path, {
      method: isWrite ? 'POST' : 'GET',
      headers: { 'X-Token': TOKEN, 'Content-Type': 'application/json' },
      body: isWrite ? JSON.stringify(payload) : undefined,
    });
    const text = await res.text();
    let data = {};
    try {
      data = text ? JSON.parse(text) : {};
    } catch {
      data = { error: text };
    }
    if (!res.ok) throw new Error(data.error || `请求失败 (${res.status})`);
    return data;
  } finally {
    if (isWrite) writing = false;
  }
}

const esc = (s) =>
  String(s == null ? '' : s).replace(/[&<>"']/g, (c) =>
    ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c])
  );

function fmtTime(ms) {
  if (!ms) return '—';
  const d = new Date(ms);
  const p = (n) => String(n).padStart(2, '0');
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

function fmtBytes(n) {
  if (!n) return '0 B';
  if (n < 1024) return n + ' B';
  if (n < 1024 * 1024) return Math.round(n / 1024) + ' KB';
  return (n / 1024 / 1024).toFixed(1) + ' MB';
}

// 窄窗口下保留盘符和最后一级目录，完整路径放 title
function shortPath(p) {
  if (!p) return '';
  const segs = p.split(/[\\/]+/).filter(Boolean);
  if (segs.length <= 2) return p;
  const isWin = /^[A-Za-z]:/.test(p);
  const last = segs[segs.length - 1];
  return isWin ? `${segs[0]}\\…\\${last}` : `…/${last}`;
}

let toastTimer = null;
function toast(msg, isError) {
  const el = $('#toast');
  el.textContent = msg;
  el.className = 'toast' + (isError ? ' error' : '');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.classList.add('hidden'), 4200);
}

/* ---------------- 数据加载 ---------------- */

async function loadState() {
  try {
    state.data = await api('/api/state');
    render();
  } catch (err) {
    toast(err.message, true);
  }
}

/* ---------------- 渲染 ---------------- */

function visibleSessions() {
  const all = state.data.sessions || [];
  const q = state.query.trim().toLowerCase();

  let list = all;
  if (state.tab === 'archived') list = all.filter((s) => s.archived);
  else if (state.tab === 'orphan') list = all.filter((s) => !s.inIndex);

  if (q) {
    list = list.filter(
      (s) =>
        (s.title || '').toLowerCase().includes(q) ||
        (s.workspacePath || '').toLowerCase().includes(q) ||
        (s.workspaceTitle || '').toLowerCase().includes(q)
    );
  }
  return list;
}

function render() {
  const d = state.data;
  if (!d) return;

  $('#homePath').textContent = shortPath(d.home);
  $('#homePath').title = d.home;
  // 数组字段一律兜底：接口异常或旧批次缺数据时也不能让整个界面抛异常
  $('#cAll').textContent = (d.sessions || []).length;
  $('#cArchived').textContent = d.archived || 0;
  $('#cOrphan').textContent = d.orphan || 0;
  $('#cTrash').textContent = (d.trash || []).length;

  renderBanner();
  renderList();
  syncSelection();
}

function renderBanner() {
  const d = state.data;
  const el = $('#banner');
  if (d.running) {
    el.className = 'banner';
    el.innerHTML = `检测到 <b>${esc(d.process)}</b> 正在运行。运行中客户端会用内存数据覆盖索引，删除会失败——请先完全退出（含托盘图标）再操作。`;
  } else {
    el.className = 'banner hidden';
  }
}

function renderList() {
  const box = $('#list');

  if (state.tab === 'trash') {
    box.innerHTML = renderTrash();
    return;
  }

  const list = visibleSessions();
  if (!list.length) {
    box.innerHTML = `<div class="empty">${
      state.query ? '没有匹配的会话' : '这里是空的'
    }</div>`;
    return;
  }

  const groups = new Map();
  for (const s of list) {
    const key = s.workspaceId || '__orphan__';
    if (!groups.has(key)) {
      groups.set(key, {
        id: s.workspaceId,
        title: s.workspaceTitle || '未分组（原工作区已删除）',
        path: s.workspacePath || '',
        items: [],
      });
    }
    groups.get(key).items.push(s);
  }

  let html = '';
  for (const g of groups.values()) {
    html += `<section class="group">
      <div class="group-head">
        <span class="group-title">${esc(g.title)}</span>
        <span class="group-path" title="${esc(g.path)}">${esc(g.path)}</span>
        <span class="group-count">${g.items.length} 个</span>
        ${
          g.id
            ? `<button class="danger small" data-ws="${esc(g.id)}" data-wsname="${esc(g.title)}" data-wscount="${g.items.length}">删除整个工作区</button>`
            : ''
        }
      </div>
      <div class="rows">${g.items.map(renderRow).join('')}</div>
    </section>`;
  }
  box.innerHTML = html;
}

function renderRow(s) {
  const checked = state.selected.has(s.id) ? ' checked' : '';
  const badges = [];
  if (s.archived) badges.push('<span class="badge archived">已归档</span>');
  if (!s.inIndex) badges.push('<span class="badge orphan">工作区已删</span>');
  if (s.pinned) badges.push('<span class="badge">置顶</span>');

  return `<div class="row${checked}">
    <input type="checkbox" data-id="${esc(s.id)}"${checked ? ' checked' : ''}>
    <span class="row-title" title="${esc(s.title)}">${esc(s.title)}</span>
    ${badges.join('')}
    <span class="row-meta">${fmtTime(s.lastTime)}</span>
    <span class="row-meta">${fmtBytes(s.bytes)}</span>
  </div>`;
}

function renderTrash() {
  const trash = state.data.trash || [];
  if (!trash.length) {
    return '<div class="empty">隔离区是空的。删除的会话会先放到这里，随时可以还原。</div>';
  }
  return trash
    .map((t) => {
      const items = t.items || [];
      const when = t.createdAt ? new Date(t.createdAt).toLocaleString('zh-CN') : t.name;
      return `<section class="group">
      <div class="group-head">
        <span class="group-title">${esc(when)}</span>
        <span class="group-path">${items.length} 个会话 · ${fmtBytes(t.bytes)}</span>
        <button class="small" data-restore="${esc(t.dir)}">还原全部</button>
        <button class="danger small" data-purge="${esc(t.dir)}">彻底清空</button>
      </div>
      <div class="rows">
        ${items
          .map(
            (it) => `<div class="row">
            <span class="row-title">${esc(it.title || it.sessionId)}</span>
            <span class="row-meta">${esc(it.sessionId.slice(0, 18))}…</span>
          </div>`
          )
          .join('')}
      </div>
    </section>`;
    })
    .join('');
}

/* ---------------- 选中状态 ---------------- */

function syncSelection() {
  $('#selCount').textContent = state.selected.size;
  $('#btnDeleteSel').disabled = state.selected.size === 0;
}

/* ---------------- 删除动作 ---------------- */

function isHard() {
  return $('#hardMode').checked;
}

async function confirmHardFirst(msg) {
  if (!confirm(msg)) return false;
  if (isHard()) {
    return confirm('再次确认：彻底删除后文件不会进隔离区，无法找回。');
  }
  return true;
}

async function doDelete(ids) {
  if (!ids.length) return;
  const mode = isHard() ? '彻底删除（不可恢复）' : '移入隔离区（可还原）';
  if (!(await confirmHardFirst(`确定要${mode} ${ids.length} 个会话吗？`))) return;

  try {
    const res = await api('/api/delete', { ids, hard: isHard() });
    let msg = `已处理 ${res.removed} 个会话`;
    if (res.trashDir) msg += `，隔离区：${res.trashDir}`;
    if (res.failed && res.failed.length) msg += `，失败 ${res.failed.length} 个`;
    toast(msg);
    state.selected.clear();
    await loadState();
  } catch (err) {
    toast(err.message, true);
  }
}

async function doDeleteWorkspace(id, name, count) {
  const ok = await confirmHardFirst(
    `将删除工作区「${name}」及其中的 ${count} 个会话。\n工作区配置和会话记录都会被移除。`
  );
  if (!ok) return;
  try {
    const res = await api('/api/delete-workspace', { workspaceId: id, hard: isHard() });
    toast(`工作区已删除，处理 ${res.removed} 个会话`);
    state.selected.clear();
    await loadState();
  } catch (err) {
    toast(err.message, true);
  }
}

async function doDeleteArchived() {
  if (!(await confirmHardFirst('确定删除全部已归档会话吗？'))) return;
  try {
    const res = await api('/api/delete-archived', { hard: isHard() });
    toast(`已删除 ${res.removed} 个已归档会话`);
    await loadState();
  } catch (err) {
    toast(err.message, true);
  }
}

async function doDeleteOrphan() {
  if (!(await confirmHardFirst('确定清空「未分组」吗？这些会话的原工作区已经被删除了。'))) return;
  try {
    const res = await api('/api/delete-orphan', { hard: isHard() });
    toast(`已清空 ${res.removed} 个未分组会话`);
    await loadState();
  } catch (err) {
    toast(err.message, true);
  }
}

async function doRestore(dir) {
  if (!confirm('把这一批会话还原到原位吗？')) return;
  try {
    const res = await api('/api/restore', { dir });
    toast(`已还原 ${res.restored} 个会话`);
    await loadState();
  } catch (err) {
    toast(err.message, true);
  }
}

async function doPurge(dir) {
  if (!confirm('彻底清空这一批隔离区？此操作不可恢复。')) return;
  try {
    await api('/api/purge', { dir });
    toast('隔离区已清空');
    await loadState();
  } catch (err) {
    toast(err.message, true);
  }
}

/* ---------------- 切换数据目录 ---------------- */

// openSwitcher 打开时才去探测目录。
// 早期实现把候选列表塞在 /api/state 里，等于每次刷新都全盘扫一遍 + 递归统计目录大小，
// 数据目录大的时候会明显卡顿，也白耗磁盘。
async function openSwitcher() {
  let list = [];
  try {
    list = (await api('/api/candidates')) || [];
  } catch (err) {
    toast(err.message, true);
    return;
  }
  const mask = document.createElement('div');
  mask.className = 'mask';
  mask.innerHTML = `<div class="dialog">
    <h3>选择数据目录</h3>
    <p class="dialog-tip">本工具扫描到以下目录，选择会话最多的那个通常是正确的。选错目录不会损坏数据，只是列表不对。</p>
    <div class="cand-list">
      ${
        list.length
          ? list
              .map(
                (c) => `<button class="cand" data-home="${esc(c.home)}">
            <span class="cand-home">${esc(c.home)}</span>
            <span class="cand-meta">${c.sessionCount} 个会话 · ${fmtBytes(c.sizeBytes)} · ${esc(c.source)}</span>
          </button>`
              )
              .join('')
          : '<div class="empty">没有扫描到可用的数据目录</div>'
      }
    </div>
    <div class="dialog-foot">
      <button id="dlgCancel">取消</button>
    </div>
  </div>`;
  document.body.appendChild(mask);

  const close = () => mask.remove();
  mask.addEventListener('click', (e) => {
    if (e.target === mask) close();
    const home = e.target.closest('.cand')?.dataset.home;
    if (home) {
      api('/api/switch-home', { home })
        .then(() => {
          close();
          return loadState();
        })
        .then(() => toast('已切换数据目录'))
        .catch((err) => toast(err.message, true));
    }
    if (e.target.id === 'dlgCancel') close();
  });
}

/* ---------------- 心跳：页面关掉后主程序自动退出 ---------------- */

function startHeartbeat() {
  const beat = () => {
    fetch('/api/ping', { method: 'POST', headers: { 'X-Token': TOKEN } }).catch(() => {});
  };
  beat();
  setInterval(beat, 5000);
}

/* ---------------- 事件绑定 ---------------- */

function bind() {
  document.querySelectorAll('.tab').forEach((el) => {
    el.addEventListener('click', () => setTab(el.dataset.tab));
  });

  $('#search').addEventListener('input', (e) => {
    state.query = e.target.value;
    renderList();
  });

  $('#btnRefresh').addEventListener('click', () => loadState());
  $('#btnSwitch').addEventListener('click', openSwitcher);
  $('#btnQuit').addEventListener('click', async () => {
    if (!confirm('退出 DSH Cleaner 吗？')) return;
    try {
      await api('/api/quit', {});
    } catch {
      /* 程序退出后请求会中断，属于正常 */
    }
    window.close();
    document.body.innerHTML =
      '<div class="empty">已退出，可以关闭这个窗口了。</div>';
  });
  $('#btnDeleteSel').addEventListener('click', () => doDelete([...state.selected]));
  $('#btnDeleteArchived').addEventListener('click', doDeleteArchived);
  $('#btnDeleteOrphan').addEventListener('click', doDeleteOrphan);

  $('#list').addEventListener('click', (e) => {
    const cb = e.target.closest('input[type=checkbox]');
    if (cb) {
      const id = cb.dataset.id;
      if (cb.checked) state.selected.add(id);
      else state.selected.delete(id);
      cb.closest('.row').classList.toggle('checked', cb.checked);
      syncSelection();
      return;
    }
    const del = e.target.closest('[data-ws]');
    if (del) {
      doDeleteWorkspace(del.dataset.ws, del.dataset.wsname, Number(del.dataset.wscount));
      return;
    }
    const restore = e.target.closest('[data-restore]');
    if (restore) {
      doRestore(restore.dataset.restore);
      return;
    }
    const purge = e.target.closest('[data-purge]');
    if (purge) doPurge(purge.dataset.purge);
  });

  syncTabs();
}

// setTab 切换标签，同时把当前标签写进 URL hash（刷新或收藏能停在原处）
function setTab(tab) {
  state.tab = tab;
  if (location.hash !== '#' + tab) history.replaceState(null, '', '#' + tab);
  syncTabs();
  renderList();
}

// syncTabs 让高亮跟着 state.tab 走，支持从 URL hash 直接进某个标签
function syncTabs() {
  document.querySelectorAll('.tab').forEach((t) => {
    t.classList.toggle('active', t.dataset.tab === state.tab);
  });
}

bind();
loadState();
startHeartbeat();
