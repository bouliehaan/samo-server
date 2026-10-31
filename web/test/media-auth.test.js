import { test } from 'node:test';
import assert from 'node:assert/strict';
import { installMediaAuth } from '../client/media-auth.js';

// Just enough of a browser for the hooks: an <img> whose src lands in an
// attribute map, a style declaration with a backgroundImage accessor on its
// prototype, and a fetch that records the headers it was given.
function fakeWindow(status = {}) {
  class Element {
    constructor() { this.attributes = {}; }
    setAttribute(name, value) { this.attributes[name] = String(value); }
    removeAttribute(name) { delete this.attributes[name]; }
  }
  class HTMLImageElement extends Element {}
  Object.defineProperty(HTMLImageElement.prototype, 'src', {
    configurable: true, enumerable: true,
    get() { return this.attributes.src ?? ''; },
    set(value) { this.attributes.src = String(value); },
  });
  class CSSStyleDeclaration {
    constructor() { this.values = {}; }
    setProperty(name, value) { this.values[name] = value; }
  }
  Object.defineProperty(CSSStyleDeclaration.prototype, 'backgroundImage', {
    configurable: true, enumerable: true,
    get() { return this.values['background-image'] ?? ''; },
    set(value) { this.values['background-image'] = value; },
  });
  let blobs = 0;
  const win = {
    location: { href: 'http://192.168.1.10:8991/listen/', origin: 'http://192.168.1.10:8991' },
    Element, HTMLImageElement, CSSStyleDeclaration,
    document: { documentElement: { style: new CSSStyleDeclaration() } },
    requests: [], revoked: [],
    async fetch(url, init) {
      win.requests.push({ url, auth: init?.headers?.Authorization });
      const code = status[new URL(url).pathname] ?? 200;
      return { ok: code < 400, status: code, blob: async () => ({ url }) };
    },
    URL: Object.assign(class extends URL {}, {
      createObjectURL: () => `blob:http://192.168.1.10:8991/${++blobs}`,
      revokeObjectURL: url => win.revoked.push(url),
    }),
  };
  return win;
}
const settle = () => new Promise(resolve => setTimeout(resolve, 0));

test('a full-size cover in a bare <img> is fetched with the bearer and drawn from a blob', async () => {
  const win = fakeWindow();
  installMediaAuth(win, 'secret');
  const img = new win.HTMLImageElement();
  img.setAttribute('src', 'http://192.168.1.10:8991/api/v1/music/songs/7/cover?width=1000');
  assert.equal(img.attributes.src, undefined, 'nothing is drawn before the credentialed fetch returns');
  await settle();
  assert.deepEqual(win.requests, [{ url: 'http://192.168.1.10:8991/api/v1/music/songs/7/cover?width=1000', auth: 'Bearer secret' }]);
  assert.equal(img.attributes.src, 'blob:http://192.168.1.10:8991/1');
  // The same cover again draws at once, from the blob already made.
  const again = new win.HTMLImageElement();
  again.src = '/api/v1/music/songs/7/cover?width=1000';
  assert.equal(again.src, 'blob:http://192.168.1.10:8991/1');
  assert.equal(win.requests.length, 1);
});

test('the bearer never goes to another origin or a non-API path', async () => {
  const win = fakeWindow();
  installMediaAuth(win, 'secret');
  const img = new win.HTMLImageElement();
  for (const src of ['https://images.example.com/api/v1/cover.jpg', '/listen/assets/logo.png', 'data:image/png;base64,AA']) {
    img.src = src;
    assert.equal(img.src, src);
  }
  await settle();
  assert.deepEqual(win.requests, []);
});

test('a cover the server refuses is set as given so the renderer sees the error', async () => {
  const win = fakeWindow({ '/api/v1/music/songs/9/cover': 404 });
  installMediaAuth(win, 'secret');
  const img = new win.HTMLImageElement();
  img.src = '/api/v1/music/songs/9/cover';
  await settle();
  assert.equal(img.src, '/api/v1/music/songs/9/cover');
});

test('a slow cover never replaces the one asked for after it', async () => {
  const win = fakeWindow();
  installMediaAuth(win, 'secret');
  const img = new win.HTMLImageElement();
  img.src = '/api/v1/music/songs/1/cover';
  img.src = '/listen/assets/placeholder.png';
  await settle();
  assert.equal(img.src, '/listen/assets/placeholder.png');
  img.src = '/api/v1/music/songs/2/cover';
  img.removeAttribute('src');
  await settle();
  assert.equal(img.attributes.src, undefined);
});

test('full-screen background layers are fetched with the bearer and redrawn once ready', async () => {
  const win = fakeWindow();
  installMediaAuth(win, 'secret');
  const style = new win.CSSStyleDeclaration();
  const value = 'url("http://192.168.1.10:8991/api/v1/music/albums/3/cover"), url("/api/v1/music/songs/4/cover")';
  style.backgroundImage = value;
  assert.equal(style.backgroundImage, 'none, none');
  await settle();
  assert.equal(style.backgroundImage, 'url("blob:http://192.168.1.10:8991/1"), url("blob:http://192.168.1.10:8991/2")');
  assert.ok(win.requests.every(r => r.auth === 'Bearer secret'));
  // A background replaced before its layers arrive is left alone.
  style.backgroundImage = 'url("/api/v1/music/songs/5/cover")';
  style.setProperty('background-image', 'linear-gradient(red, blue)');
  await settle();
  assert.equal(style.values['background-image'], 'linear-gradient(red, blue)');
});

test('old blobs are revoked once the cache is full', async () => {
  const win = fakeWindow();
  installMediaAuth(win, 'secret', { limit: 2 });
  const img = new win.HTMLImageElement();
  for (const id of [1, 2, 3]) { img.src = `/api/v1/music/songs/${id}/cover`; await settle(); }
  assert.deepEqual(win.revoked, ['blob:http://192.168.1.10:8991/1']);
});
