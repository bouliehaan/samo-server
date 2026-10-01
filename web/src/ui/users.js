import { escapeHTML, attr } from './html.js';
import { api, currentUser, setCurrentUser } from './auth.js';

export async function renderUsers(main) {
  const data = await api('/api/v1/users');
  let selectedID = null;
  let creating = false;
  let query = '';
  let busy = false;
  main.innerHTML = `<section class="view">
    <div class="view-head"><h1>USERS</h1><div class="view-actions"><button class="btn primary btn-small" id="addUser">+ ADD USER</button></div></div>
    <div class="users-layout">
      <div class="panel users-directory">
        <div class="panel-head"><span>// accounts</span><span id="userCount"></span></div>
        <label class="field users-search"><span class="field-label">FIND A USER</span><input id="userSearch" type="search" placeholder="Name or username" autocomplete="off"></label>
        <div class="list" id="userList"></div>
      </div>
      <div class="panel users-detail" id="userDetail"></div>
    </div>
  </section>`;
  const list = main.querySelector('#userList');
  const detail = main.querySelector('#userDetail');
  function directory() {
    main.querySelector('#userCount').textContent = data.items.length;
    const matches = data.items.filter(u => `${u.username} ${u.displayName}`.toLowerCase().includes(query.toLowerCase()));
    list.innerHTML = matches.map((u, i) => `<button type="button" class="list-row users-row${u.id === selectedID ? ' selected' : ''}" data-user="${attr(u.id)}" aria-pressed="${u.id === selectedID}">
      <span class="num">${String(i + 1).padStart(2, '0')}</span>
      <span class="main"><span class="name">${escapeHTML(u.displayName || u.username)}</span><span class="meta">@${escapeHTML(u.username)}${u.id === currentUser.id ? ' · YOU' : ''}</span></span>
      <span class="users-role">${u.id === 'user-server' ? 'SYSTEM' : escapeHTML(u.role).toUpperCase()}</span><span class="users-arrow" aria-hidden="true">→</span>
    </button>`).join('') || '<div class="empty-state">// no matching users</div>';
  }
  function field(name, title, value = '', type = 'text', required = false) {
    return `<label class="field"><span class="field-label">${title}</span><input name="${name}" type="${type}" value="${attr(value)}" ${required ? 'required' : ''} ${type === 'password' ? 'autocomplete="new-password"' : ''}></label>`;
  }
  function showDetail() {
    if (!selectedID && !creating) {
      detail.innerHTML = '<div class="panel-head"><span>// account details</span></div><div class="empty-state">// select a user to manage their account</div>';
      return;
    }
    const user = data.items.find(u => u.id === selectedID);
    if (user?.id === 'user-server') {
      detail.innerHTML = '<div class="panel-head"><span>// server identity</span><span>SYSTEM</span></div><div class="empty-state">// managed by server configuration<br>This identity belongs to the server and cannot be edited or removed here.</div>';
      return;
    }
    const self = user?.id === currentUser.id;
    detail.innerHTML = `<div class="panel-head"><span>// ${creating ? 'new account' : 'edit account'}</span><span>${self ? 'YOUR ACCOUNT' : 'ACCESS'}</span></div>
      <form class="settings-form" id="manageUserForm">
        <div class="form-grid">${field('username', 'USERNAME', user?.username || '', 'text', true)}${field('displayName', 'DISPLAY NAME', user?.displayName || '')}
          <label class="field"><span class="field-label">ROLE</span><select name="role" ${self ? 'disabled' : ''}><option value="user" ${user?.role !== 'admin' ? 'selected' : ''}>User</option><option value="admin" ${user?.role === 'admin' ? 'selected' : ''}>Admin</option></select></label>
          ${field('password', creating ? 'PASSWORD' : 'NEW PASSWORD', '', 'password', creating)}</div>
        <p class="panel-sub">${creating ? 'Users open the web player. Admins can also manage the server.' : 'Leave the password blank to keep it. Resetting another user’s password signs out their devices.'}</p>
        <div class="actions"><button class="btn primary" type="submit">${creating ? 'CREATE USER' : 'SAVE CHANGES'}</button><button class="btn ghost" type="button" id="cancelUserEdit">CANCEL</button></div>
        <div class="status-line" id="userResult" role="status" hidden></div>
        ${!creating && !self ? '<div class="users-danger"><span class="panel-sub">Remove this account and its listening history.</span><button class="btn danger btn-small" type="button" id="deleteUser">DELETE USER</button></div>' : ''}
      </form>`;
  }
  function message(text, error = false) {
    const result = detail.querySelector('#userResult');
    result.hidden = false;
    result.classList.toggle('error', error);
    result.textContent = text;
  }
  function setBusy(value) {
    busy = value;
    main.querySelectorAll('button, input, select').forEach(el => { el.disabled = value; });
    if (!value && selectedID === currentUser.id) detail.querySelector('[name="role"]').disabled = true;
  }
  directory(); showDetail();
  main.querySelector('#userSearch').addEventListener('input', e => { query = e.target.value; directory(); });
  main.querySelector('#addUser').addEventListener('click', () => {
    creating = true; selectedID = null; directory(); showDetail(); detail.querySelector('input').focus();
  });
  list.addEventListener('click', e => {
    const row = e.target.closest('[data-user]');
    if (!row || busy) return;
    selectedID = row.dataset.user; creating = false; directory(); showDetail();
  });
  detail.addEventListener('submit', async e => {
    e.preventDefault();
    if (busy) return;
    const fields = new FormData(e.target);
    const body = { username: fields.get('username').trim(), displayName: fields.get('displayName').trim() };
    if (fields.has('role')) body.role = fields.get('role');
    if (fields.get('password')) body.password = fields.get('password');
    setBusy(true);
    try {
      const user = await api('/api/v1/users' + (creating ? '' : '/' + encodeURIComponent(selectedID)), { method: creating ? 'POST' : 'PATCH', body });
      const existing = data.items.find(u => u.id === user.id);
      if (existing) Object.assign(existing, user); else data.items.push(user);
      data.items.sort((a,b) => a.username.localeCompare(b.username));
      if (user.id === currentUser.id) { setCurrentUser(user); document.getElementById('authUser').textContent = user.username.toUpperCase(); }
      const created = creating;
      selectedID = user.id; creating = false; directory(); showDetail(); message(created ? 'User created.' : 'Changes saved.');
    } catch (err) { message(err.message, true); }
    finally { setBusy(false); }
  });
  detail.addEventListener('click', async e => {
    if (busy) return;
    if (e.target.closest('#cancelUserEdit')) { creating = false; selectedID = null; directory(); showDetail(); }
    if (!e.target.closest('#deleteUser')) return;
    const user = data.items.find(u => u.id === selectedID);
    if (!confirm(`Delete ${user.username}? Their account, credentials, and personal listening history will be permanently removed.`)) return;
    setBusy(true);
    try {
      await api('/api/v1/users/' + encodeURIComponent(user.id), { method: 'DELETE' });
      data.items = data.items.filter(u => u.id !== user.id); selectedID = null; directory(); showDetail();
    } catch (err) { message(err.message, true); }
    finally { setBusy(false); }
  });
}
