import './server-host.css';
import { installRandomUUID } from './server-compat.js';
installRandomUUID(window.crypto);

// Authenticate before importing the renderer (its stores hydrate at import time).
// A per-user record ID keeps the desktop client's caches separated by account.
async function boot() {
  const token = localStorage.getItem('samo-token');
  if (!token) return signIn();
  const response = await fetch('/api/v1/users/me', { headers: { Authorization: 'Bearer ' + token } });
  if (response.status === 401) return signIn();
  if (!response.ok) throw new Error('Unable to connect to Samo. Reload to try again.');
  const user = await response.json();
  const id = 'hosted-' + user.id;
  const server = { id, name: 'Samo', url: location.origin, type: 'samo', credential: token,
    userId: user.id, username: user.username, isAdmin: user.role === 'admin' };
  const previousUser = localStorage.getItem('samo-web-user');
  if (previousUser !== user.id) {
    // The renderer persists queries under one IndexedDB key across accounts.
    const { delMany } = await import('idb-keyval');
    await delMany(['samo', 'player-store', 'player-store-queue', 'audiobook-store', 'podcast-store']);
    for (const key of ['last-playback-session-store', 'recent-items-store', 'library-favorites-store', 'hidden-home-items-store', 'audiobook-store', 'podcast-store']) localStorage.removeItem(key);
    localStorage.setItem('samo-web-user', user.id);
  }
  localStorage.setItem('store_authentication', JSON.stringify({ version: 7, state: {
    currentServer: server, activeMusicServerId: id, serverList: { [id]: server },
  } }));
  window.SERVER_URL = location.origin;
  window.SERVER_NAME = 'Samo';
  window.SERVER_TYPE = 'samo';
  window.SERVER_LOCK = true;
  const controls = document.createElement('div');
  controls.className = 'samo-host-session';
  if (user.role === 'admin') {
    const admin = document.createElement('a');
    admin.href = '/app'; admin.textContent = 'Server admin'; controls.append(admin);
  }
  const logout = document.createElement('button');
  logout.textContent = 'Sign out';
  logout.onclick = async () => {
    logout.disabled = true;
    try {
      await fetch('/api/v1/users/me/tokens/current', { method: 'DELETE', headers: { Authorization: 'Bearer ' + token } });
    } finally { signIn(); }
  };
  controls.append(logout); document.body.append(controls);
  window.addEventListener('storage', e => { if (e.key === 'samo-token' && e.newValue !== token) location.reload(); });
  await import('./main.tsx');
}
function signIn() {
  localStorage.removeItem('samo-token');
  localStorage.removeItem('store_authentication');
  location.replace('/login?next=' + encodeURIComponent('/listen/' + location.hash));
}
boot().catch(error => {
  document.getElementById('root').textContent = error.message || 'Unable to load Samo. Reload to try again.';
});
