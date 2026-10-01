// Entry point for /pair, where a signed-in person approves a TV. The TV shows a
// short code and this address, also as a QR code that carries the code in the
// fragment (#code=ABCD-EFGH), which never reaches the server or its logs.
// Approval uses this browser's own sign-in; the TV then collects a device token
// of its own by polling. The protocol is in internal/api/device_pairing.go.
import './login.css';

const code = document.getElementById('code');
const message = document.getElementById('message');
const approve = document.getElementById('approve');
const deny = document.getElementById('deny');
const token = localStorage.getItem('samo-token');
let signedIn = false;
code.value = new URLSearchParams(location.hash.slice(1)).get('code') || '';
// A second code (a TV asked for a new one) often arrives in the same tab, and
// a fragment-only change does not reload the page — so it must reset it, or
// the old code and its "declined" stay on screen with the buttons disabled.
window.addEventListener('hashchange', () => {
  const next = new URLSearchParams(location.hash.slice(1)).get('code');
  if (next === null) return;
  code.value = next;
  code.disabled = false;
  message.textContent = '';
  approve.disabled = deny.disabled = !signedIn;
});
function signIn() {
  const next = '/pair#code=' + encodeURIComponent(code.value);
  location.assign('/login?next=' + encodeURIComponent(next));
}
async function decide(allow) {
  if (!code.value.trim()) { message.textContent = 'Enter the code displayed on your TV.'; return; }
  approve.disabled = deny.disabled = true;
  message.textContent = 'Connecting…';
  try {
    const response = await fetch('/api/v1/auth/device/approve', {
      method: 'POST', headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + token },
      body: JSON.stringify({ user_code: code.value, approve: allow }),
    });
    if (response.status === 401) { signIn(); return; }
    // The server's own 429 text is the machine word "slow_down".
    if (response.status === 429) throw new Error('Too many attempts. Wait a minute, then try again.');
    const body = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(body.error || 'Could not approve the device.');
    message.textContent = allow ? 'Approved. Your TV will sign in automatically. You can close this page.' : 'Request declined. You can close this page.';
    code.disabled = true;
  } catch (error) {
    message.textContent = error.message;
    approve.disabled = deny.disabled = false;
  }
}
approve.addEventListener('click', () => decide(true));
deny.addEventListener('click', () => decide(false));
code.addEventListener('keydown', (event) => { if (event.key === 'Enter' && !approve.disabled) decide(true); });
if (!token) {
  signIn();
} else {
  fetch('/api/v1/users/me', { headers: { Authorization: 'Bearer ' + token } })
    .then(async (response) => {
      if (response.status === 401) { signIn(); return; }
      if (!response.ok) throw new Error('Could not verify your account. Reload to try again.');
      const user = await response.json();
      document.getElementById('account').textContent = 'Signed in as ' + (user.displayName || user.username);
      signedIn = true;
      approve.disabled = deny.disabled = false;
    })
    .catch((error) => { message.textContent = error.message; });
}
