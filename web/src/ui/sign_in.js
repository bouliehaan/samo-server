// Settings → SIGN-IN: the identity providers samo accepts besides its own
// passwords. One form for both, because the account rules (who gets an
// account, who is an admin) apply to both. The protocol and the rules are in
// internal/extauth and internal/users/identities.go.
import { api } from './auth.js';
import { attr, escapeHTML } from './html.js';
import { fieldHTML, textAreaHTML } from './composer.js';

function checkbox(id, label, checked) {
  return `<label class="field checkbox full"><input id="${attr(id)}" type="checkbox"${checked ? ' checked' : ''}><span>${escapeHTML(label)}</span></label>`;
}

function value(root, id) {
  const el = root.querySelector('#' + id);
  return el ? el.value.trim() : '';
}

function checked(root, id) {
  const el = root.querySelector('#' + id);
  return !!(el && el.checked);
}

export async function renderSignInSettings(root) {
  let config = await api('/api/v1/auth/providers/config');

  function draw() {
    const o = config.oidc || {};
    const l = config.ldap || {};
    root.innerHTML = `<div class="account-layout">
      <form class="panel panel-wide" id="signInForm">
        <div class="panel-head"><span>// accounts</span><span>${o.enabled || l.enabled ? 'PROVIDERS ON' : 'PASSWORDS ONLY'}</span></div>
        <p class="panel-sub">Local accounts always sign in with their password first, so the admin made in setup still works if a provider is down or misconfigured.</p>
        <div class="form-grid">
          ${fieldHTML('siPublicURL', 'Public URL', 'https://samo.example.com — where browsers reach samo', 'text', config.publicUrl, 'full')}
          ${checkbox('siAutoProvision', 'Create a samo account for anyone the provider signs in', config.autoProvision)}
          ${checkbox('siLinkByUsername', 'Adopt an existing samo account with the same username on first sign-in', config.linkByUsername)}
          ${fieldHTML('siAdminGroup', 'Admin group', 'members are admins, others users; blank leaves roles alone', 'text', config.adminGroup)}
          ${fieldHTML('siRequiredGroup', 'Required group', 'blank lets in whoever the provider lets in', 'text', config.requiredGroup)}
        </div>

        <div class="panel-head"><span>// openid connect</span><span>${o.enabled ? 'ON' : 'OFF'}</span></div>
        <p class="panel-sub">Register this redirect URI with the provider: <code>${escapeHTML(config.redirectUri || '')}</code></p>
        <div class="form-grid">
          ${checkbox('siOIDCEnabled', 'Offer “sign in with …” on the login page', o.enabled)}
          ${fieldHTML('siOIDCLabel', 'Button label', 'authentik', 'text', o.label)}
          ${fieldHTML('siOIDCIssuer', 'Issuer URL', 'https://auth.example.com/application/o/samo/', 'text', o.issuer, 'full')}
          ${fieldHTML('siOIDCClientID', 'Client ID', 'client id', 'text', o.clientId)}
          ${fieldHTML('siOIDCClientSecret', 'Client secret', config.hasClientSecret ? 'leave blank to keep current' : 'client secret', 'password', '')}
          ${fieldHTML('siOIDCScopes', 'Scopes', 'profile email', 'text', o.scopes)}
          ${fieldHTML('siOIDCUsernameClaim', 'Username claim', 'preferred_username', 'text', o.usernameClaim)}
          ${fieldHTML('siOIDCGroupsClaim', 'Groups claim', 'groups', 'text', o.groupsClaim)}
          ${textAreaHTML('siOIDCCACert', 'Extra CA certificate (PEM)', 'only if samo cannot verify the provider’s certificate, e.g. a Cloudflare Origin CA', o.caCert, 'full')}
        </div>
        <div class="actions"><button class="btn ghost" type="button" data-signin-test="oidc">TEST CONNECTION</button></div>

        <div class="panel-head"><span>// ldap</span><span>${l.enabled ? 'ON' : 'OFF'}</span></div>
        <p class="panel-sub">Checked whenever a password is not a local account’s, from every app that signs in with a username and password.</p>
        <div class="form-grid">
          ${checkbox('siLDAPEnabled', 'Accept directory passwords', l.enabled)}
          ${fieldHTML('siLDAPURL', 'URL', 'ldaps://outpost:6636', 'text', l.url)}
          ${checkbox('siLDAPStartTLS', 'StartTLS (ldap:// only)', l.startTls)}
          ${checkbox('siLDAPInsecure', 'Accept any certificate (LAN-only outposts)', l.insecureSkipVerify)}
          ${fieldHTML('siLDAPBaseDN', 'Base DN', 'dc=ldap,dc=goauthentik,dc=io', 'text', l.baseDn, 'full')}
          ${fieldHTML('siLDAPBindDN', 'Bind DN', 'cn=samo-search,ou=users,dc=ldap,dc=goauthentik,dc=io', 'text', l.bindDn, 'full')}
          ${fieldHTML('siLDAPBindPassword', 'Bind password', config.hasBindPassword ? 'leave blank to keep current' : 'service account password', 'password', '')}
          ${fieldHTML('siLDAPUserFilter', 'User filter', '(&(objectClass=user)(cn={username}))', 'text', l.userFilter, 'full')}
          ${fieldHTML('siLDAPUsernameAttr', 'Username attribute', 'cn', 'text', l.usernameAttr)}
          ${fieldHTML('siLDAPDisplayNameAttr', 'Display name attribute', 'displayName', 'text', l.displayNameAttr)}
          ${fieldHTML('siLDAPEmailAttr', 'Email attribute', 'mail', 'text', l.emailAttr)}
          ${fieldHTML('siLDAPGroupsAttr', 'Groups attribute', 'memberOf', 'text', l.groupsAttr)}
          ${fieldHTML('siLDAPSubjectAttr', 'Unique ID attribute', 'blank: entryUUID, then uid, then the DN', 'text', l.subjectAttr)}
          ${textAreaHTML('siLDAPCACert', 'Extra CA certificate (PEM)', 'the outpost’s certificate or its CA', l.caCert, 'full')}
          ${fieldHTML('siLDAPTestUser', 'Test with username', 'optional: look someone up', 'text', '')}
        </div>
        <div class="actions"><button class="btn primary" type="submit">SAVE SIGN-IN SETTINGS</button><button class="btn ghost" type="button" data-signin-test="ldap">TEST CONNECTION</button></div>
        <div class="status-line" id="signInMessage" role="status" hidden></div>
      </form>
    </div>`;
  }

  function message(text, error) {
    const line = root.querySelector('#signInMessage');
    line.hidden = false;
    line.classList.toggle('error', !!error);
    line.textContent = text;
  }

  function collect() {
    return {
      publicUrl: value(root, 'siPublicURL'),
      autoProvision: checked(root, 'siAutoProvision'),
      linkByUsername: checked(root, 'siLinkByUsername'),
      adminGroup: value(root, 'siAdminGroup'),
      requiredGroup: value(root, 'siRequiredGroup'),
      oidc: {
        enabled: checked(root, 'siOIDCEnabled'),
        label: value(root, 'siOIDCLabel'),
        issuer: value(root, 'siOIDCIssuer'),
        clientId: value(root, 'siOIDCClientID'),
        clientSecret: value(root, 'siOIDCClientSecret'),
        scopes: value(root, 'siOIDCScopes'),
        usernameClaim: value(root, 'siOIDCUsernameClaim'),
        groupsClaim: value(root, 'siOIDCGroupsClaim'),
        caCert: value(root, 'siOIDCCACert'),
      },
      ldap: {
        enabled: checked(root, 'siLDAPEnabled'),
        url: value(root, 'siLDAPURL'),
        startTls: checked(root, 'siLDAPStartTLS'),
        insecureSkipVerify: checked(root, 'siLDAPInsecure'),
        baseDn: value(root, 'siLDAPBaseDN'),
        bindDn: value(root, 'siLDAPBindDN'),
        // Not trimmed: a password may end in a space.
        bindPassword: root.querySelector('#siLDAPBindPassword').value,
        userFilter: value(root, 'siLDAPUserFilter'),
        usernameAttr: value(root, 'siLDAPUsernameAttr'),
        displayNameAttr: value(root, 'siLDAPDisplayNameAttr'),
        emailAttr: value(root, 'siLDAPEmailAttr'),
        groupsAttr: value(root, 'siLDAPGroupsAttr'),
        subjectAttr: value(root, 'siLDAPSubjectAttr'),
        caCert: value(root, 'siLDAPCACert'),
      },
    };
  }

  draw();
  root.addEventListener('submit', async (event) => {
    if (event.target.id !== 'signInForm') return;
    event.preventDefault();
    try {
      config = await api('/api/v1/auth/providers/config', { method: 'PUT', body: collect() });
      draw();
      message('Saved.');
    } catch (err) { message(err.message, true); }
  });
  root.addEventListener('click', async (event) => {
    const button = event.target.closest('[data-signin-test]');
    if (!button) return;
    const provider = button.dataset.signinTest;
    message('Testing the saved settings…');
    try {
      const result = await api('/api/v1/auth/providers/test', {
        method: 'POST',
        body: { provider, username: provider === 'ldap' ? value(root, 'siLDAPTestUser') : '' },
      });
      let text = result.message;
      if (result.identity) {
        const groups = (result.identity.groups || []).join(', ') || 'no groups';
        text += ` · ${result.identity.displayName || ''} <${result.identity.email || 'no mail'}> · ${groups} · id ${result.identity.subject}`;
      }
      message(text, !result.ok);
    } catch (err) { message(err.message, true); }
  });
}
