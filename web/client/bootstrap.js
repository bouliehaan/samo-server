import './server-host.css';
import { installRandomUUID } from './server-compat.js';
import { installMediaAuth } from './server-media-auth.js';
installRandomUUID(window.crypto);

// Authenticate before importing the renderer (its stores hydrate at import time).
// A per-user record ID keeps the desktop client's caches separated by account.
async function boot() {
  const token = localStorage.getItem('samo-token');
  if (!token) return signIn();
  const response = await fetch('/api/v1/users/me', { headers: { Authorization: 'Bearer ' + token } });
  if (response.status === 401) return signIn();
  if (!response.ok) throw new Error('Unable to connect to Samo. Reload to try again.');
  installMediaAuth(window, token);
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
  // Items for the S menu (web-client-patches.mjs puts them in AppMenu): the
  // dashboard for admins, and signing out.
  let signingOut = false;
  window.SAMO_HOST_MENU = [
    ...(user.role === 'admin'
      ? [{ id: 'server-admin', icon: 'server', label: 'Server admin', onClick: () => location.assign('/app') }]
      : []),
    { id: 'sign-out', icon: 'signOut', label: 'Sign out', onClick: async () => {
      if (signingOut) return;
      signingOut = true;
      try {
        await fetch('/api/v1/users/me/tokens/current', { method: 'DELETE', headers: { Authorization: 'Bearer ' + token } });
      } finally { signIn('local=1&'); }
    } },
  ];
  window.addEventListener('storage', e => { if (e.key === 'samo-token' && e.newValue !== token) location.reload(); });
  // A deploy renames the hashed chunks. A tab opened before it still asks for
  // the old names, and the 404 left the lazily loaded audio player unmounted:
  // play did nothing until a manual reload. Reload once instead (the queue is in
  // IndexedDB). The flag clears after a minute, so the next deploy reloads too,
  // while a build that is really missing a chunk fails once rather than looping.
  window.addEventListener('vite:preloadError', e => {
    if (sessionStorage.getItem('samo-stale-reload')) return;
    e.preventDefault();
    sessionStorage.setItem('samo-stale-reload', '1');
    location.reload();
  });
  await import('./main.tsx');
  setTimeout(() => sessionStorage.removeItem('samo-stale-reload'), 60_000);
}
// After a sign-out, local=1 asks /login for its form: with single sign-on
// configured it would otherwise send the browser straight back in.
function signIn(extra = '') {
  localStorage.removeItem('samo-token');
  localStorage.removeItem('store_authentication');
  location.replace('/login?' + extra + 'next=' + encodeURIComponent('/listen/' + location.hash));
}
boot().catch(error => {
  document.getElementById('root').textContent = error.message || 'Unable to load Samo. Reload to try again.';
});
