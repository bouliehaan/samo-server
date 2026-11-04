import { test } from 'node:test';
import assert from 'node:assert/strict';
import { backTarget, isRootPath, tabForPath, TAB_ROUTES } from '../client/mobile/routes.js';
import { installLongPress, isAppleTouch } from '../client/touch.js';
import { installMediaAuth } from '../client/media-auth.js';

test('every page lights the tab it belongs to', () => {
  assert.equal(tabForPath('/'), 'home');
  assert.equal(tabForPath('/library/albums/album_1'), 'home');
  assert.equal(tabForPath('/library/album-artists/a/discography'), 'home');
  assert.equal(tabForPath('/podcasts'), 'podcasts');
  assert.equal(tabForPath('/podcasts/podcast_1'), 'podcasts');
  assert.equal(tabForPath('/audiobooks/audiobook_1'), 'audiobooks');
  assert.equal(tabForPath('/playlists/playlist_1/songs'), 'playlists');
  assert.equal(tabForPath('/radio'), 'radio');
  // A path that only starts with a tab's name is not under it.
  assert.equal(tabForPath('/radios-of-the-world'), 'home');
  assert.equal(tabForPath('/settings'), null);
  assert.equal(tabForPath('/search/song'), null);
});

test('only the top of a tab, or a page with nothing above it, has no back button', () => {
  for (const root of Object.values(TAB_ROUTES)) assert.ok(isRootPath(root), root);
  assert.ok(isRootPath('/settings'));
  assert.ok(isRootPath('/music'));
  assert.ok(!isRootPath('/podcasts/podcast_1'));
  assert.ok(!isRootPath('/library/albums'));
});

test('back is history when there is some, and the tab otherwise', () => {
  assert.equal(backTarget('/podcasts/podcast_1', 'k2j3h4'), -1);
  // Opened straight onto an episode (a shared link, a home-screen launch):
  // going back would leave samo, so it goes up to Podcasts instead.
  assert.equal(backTarget('/podcasts/podcast_1', 'default'), '/podcasts');
  assert.equal(backTarget('/library/albums/album_1', 'default'), '/');
  assert.equal(backTarget('/search/song', 'default'), '/');
});

test('iPhones and iPads are recognised, including an iPad asking for the desktop site', () => {
  assert.ok(isAppleTouch({ userAgent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X)', maxTouchPoints: 5 }));
  assert.ok(isAppleTouch({ userAgent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)', maxTouchPoints: 5 }));
  assert.ok(!isAppleTouch({ userAgent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)', maxTouchPoints: 0 }));
  assert.ok(!isAppleTouch({ userAgent: 'Mozilla/5.0 (Linux; Android 14; Pixel 7)', maxTouchPoints: 5 }));
});

// A document that records listeners, dispatches to them, and a clock we move.
function fakeTouchWindow() {
  const listeners = {};
  let now = 0;
  const timers = [];
  const doc = {
    addEventListener(type, fn) { (listeners[type] ||= []).push(fn); },
    fire(type, event) { for (const fn of listeners[type] || []) fn(event); return event; },
  };
  class MouseEvent {
    constructor(type, init) { Object.assign(this, init, { type, defaultPrevented: false }); }
    preventDefault() { this.defaultPrevented = true; }
  }
  const win = {
    document: doc, MouseEvent,
    setTimeout(fn, ms) { const t = { fn, at: now + ms }; timers.push(t); return t; },
    clearTimeout(t) { const i = timers.indexOf(t); if (i >= 0) timers.splice(i, 1); },
    advance(ms) {
      now += ms;
      for (const t of [...timers]) if (t.at <= now) { timers.splice(timers.indexOf(t), 1); t.fn(); }
    },
  };
  const realNow = Date.now;
  Date.now = () => now;
  return { win, doc, restore: () => { Date.now = realNow; } };
}

function row(onMenu) {
  return {
    closest: () => null,
    dispatchEvent(event) { onMenu(event); doc().fire('contextmenu', event); return !event.defaultPrevented; },
  };
  function doc() { return row.doc; }
}

function click() {
  return { stopped: false, defaultPrevented: false, preventDefault() { this.defaultPrevented = true; }, stopPropagation() { this.stopped = true; } };
}

test('a held finger opens the menu where it is held, and the lift does not also tap', () => {
  const { win, doc, restore } = fakeTouchWindow();
  try {
    installLongPress(win);
    const menus = [];
    row.doc = doc;
    const target = row(event => { menus.push(event); event.preventDefault(); });
    doc.fire('touchstart', { target, touches: [{ clientX: 40, clientY: 300 }] });
    win.advance(499);
    assert.equal(menus.length, 0, 'not yet a long press');
    win.advance(1);
    assert.equal(menus.length, 1);
    assert.equal(menus[0].type, 'contextmenu');
    assert.equal(menus[0].clientX, 40);
    assert.equal(menus[0].clientY, 300);
    doc.fire('touchend', {});
    const lift = doc.fire('click', click());
    assert.ok(lift.stopped && lift.defaultPrevented, 'the click from lifting the finger is eaten');
    // The next ordinary tap goes through.
    win.advance(1000);
    const tap = doc.fire('click', click());
    assert.ok(!tap.stopped);
  } finally { restore(); }
});

test('a scroll, a short tap, or the browser opening its own menu is not a long press', () => {
  const { win, doc, restore } = fakeTouchWindow();
  try {
    installLongPress(win);
    const menus = [];
    row.doc = doc;
    const target = row(event => { menus.push(event); event.preventDefault(); });
    doc.fire('touchstart', { target, touches: [{ clientX: 40, clientY: 300 }] });
    doc.fire('touchmove', { touches: [{ clientX: 40, clientY: 330 }] });
    win.advance(800);
    assert.equal(menus.length, 0, 'moved more than the slop: a scroll');

    doc.fire('touchstart', { target, touches: [{ clientX: 40, clientY: 300 }] });
    win.advance(200);
    doc.fire('touchend', {});
    win.advance(800);
    assert.equal(menus.length, 0, 'lifted early: a tap');
    assert.ok(!doc.fire('click', click()).stopped, 'and the tap is left alone');

    doc.fire('touchstart', { target, touches: [{ clientX: 40, clientY: 300 }] });
    win.advance(300);
    doc.fire('contextmenu', { type: 'contextmenu' });
    win.advance(800);
    assert.equal(menus.length, 0, 'the platform already opened a menu');

    const field = { closest: sel => (sel.includes('input') ? {} : null) };
    doc.fire('touchstart', { target: field, touches: [{ clientX: 40, clientY: 300 }] });
    win.advance(800);
    assert.equal(menus.length, 0, 'text fields keep their own press-and-hold');
  } finally { restore(); }
});

test('lock-screen artwork is drawn from a credentialed blob, and refreshed when it lands', async () => {
  class Element { setAttribute() {} removeAttribute() {} }
  class HTMLImageElement extends Element {}
  Object.defineProperty(HTMLImageElement.prototype, 'src', { configurable: true, get() { return ''; }, set() {} });
  class CSSStyleDeclaration { setProperty() {} }
  class MediaMetadata {
    constructor(init) { Object.assign(this, init); this.artwork = (init.artwork || []).map(a => ({ ...a, src: new URL(a.src, 'http://10.0.0.2:6969/listen/').href })); }
  }
  class MediaSession {}
  const shown = [];
  Object.defineProperty(MediaSession.prototype, 'metadata', { configurable: true, get() { return shown.at(-1); }, set(v) { shown.push(v); } });
  const requests = [];
  const win = {
    location: { href: 'http://10.0.0.2:6969/listen/', origin: 'http://10.0.0.2:6969' },
    Element, HTMLImageElement, CSSStyleDeclaration, MediaMetadata,
    navigator: { mediaSession: new MediaSession() },
    document: { documentElement: { style: new CSSStyleDeclaration() } },
    async fetch(url, init) { requests.push([url, init.headers.Authorization]); return { ok: true, blob: async () => ({}) }; },
    URL: Object.assign(class extends URL {}, { createObjectURL: () => 'blob:http://10.0.0.2:6969/1', revokeObjectURL() {} }),
  };
  installMediaAuth(win, 'secret');
  win.navigator.mediaSession.metadata = new MediaMetadata({
    title: 'Track 1', artist: 'Aurora Fields', album: 'Night Drive',
    artwork: [{ src: '/api/v1/music/songs/1/cover', type: 'image/png' }],
  });
  assert.equal(shown.length, 1);
  assert.equal(shown[0].title, 'Track 1');
  assert.deepEqual(shown[0].artwork, [], 'nothing the OS would fetch without the bearer');
  await new Promise(resolve => setTimeout(resolve, 0));
  await new Promise(resolve => setTimeout(resolve, 0));
  assert.deepEqual(requests, [['http://10.0.0.2:6969/api/v1/music/songs/1/cover', 'Bearer secret']]);
  assert.equal(shown.length, 2);
  assert.equal(shown[1].artwork[0].src, 'blob:http://10.0.0.2:6969/1');
  assert.equal(shown[1].album, 'Night Drive');

  // Artwork from anywhere else is the OS's to fetch.
  win.navigator.mediaSession.metadata = new MediaMetadata({ title: 'KEXP', artwork: [{ src: 'https://cdn.example/kexp.png' }] });
  assert.equal(shown.at(-1).artwork[0].src, 'https://cdn.example/kexp.png');
});
