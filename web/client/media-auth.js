// On the desktop the main process puts the bearer on every samo /api/v1/
// request (samo-media-auth.ts), so the renderer draws artwork from plain,
// token-free URLs. A browser has no such hook — a service worker would be one,
// but those need a secure context and the player is reached over plain-HTTP LAN
// addresses — so those URLs go out with no credential and draw as broken
// images. The renderer's shared Image component hides this behind a second,
// header-carrying fetch after the 401; the sidebar and full-screen artwork are
// bare <img>s and CSS backgrounds with no such fallback.
//
// This is that hook for the browser: a same-origin /api/v1/ URL handed to an
// <img> or a background-image is fetched with the bearer and drawn from a blob
// of the response. Fetches still go through the HTTP cache, which the server
// opts into for artwork.
export function installMediaAuth(win, token, { limit = 400 } = {}) {
  const blobs = new Map(); // absolute URL -> blob URL, least recently used first
  const loading = new Map(); // absolute URL -> Promise<blob URL>

  function mediaUrl(value) {
    if (typeof value !== 'string' || value.startsWith('blob:') || value.startsWith('data:')) return null;
    try {
      const url = new win.URL(value, win.location.href);
      // Never hand the bearer to another host.
      return url.origin === win.location.origin && url.pathname.startsWith('/api/v1/') ? url.href : null;
    } catch { return null; }
  }
  function cached(url) {
    const blob = blobs.get(url);
    if (blob) { blobs.delete(url); blobs.set(url, blob); }
    return blob;
  }
  function load(url) {
    if (blobs.has(url)) return Promise.resolve(cached(url));
    if (!loading.has(url)) {
      loading.set(url, win.fetch(url, { headers: { Authorization: 'Bearer ' + token } })
        .then(response => {
          if (!response.ok) throw new Error(`${response.status} ${url}`);
          return response.blob();
        })
        .then(body => {
          const blob = win.URL.createObjectURL(body);
          blobs.set(url, blob);
          // An element already drawn from a revoked blob keeps its picture.
          for (const [old, oldBlob] of blobs) {
            if (blobs.size <= limit) break;
            blobs.delete(old); win.URL.revokeObjectURL(oldBlob);
          }
          return blob;
        })
        .finally(() => loading.delete(url)));
    }
    return loading.get(url);
  }

  // <img>: the element keeps what it is showing until the blob is ready. A URL
  // that cannot be fetched is set as given, so the error the renderer listens
  // for still fires.
  const wanted = new WeakMap(); // img -> the URL it was last asked to show
  const imageSrc = Object.getOwnPropertyDescriptor(win.HTMLImageElement.prototype, 'src');
  const setAttribute = win.Element.prototype.setAttribute;
  function showImage(img, value, apply) {
    const url = mediaUrl(String(value));
    wanted.set(img, url);
    if (!url) return apply(value);
    const blob = cached(url);
    if (blob) return apply(blob);
    load(url).then(blob => { if (wanted.get(img) === url) apply(blob); },
      () => { if (wanted.get(img) === url) apply(value); });
  }
  Object.defineProperty(win.HTMLImageElement.prototype, 'src', { ...imageSrc, set(value) {
    showImage(this, value, v => imageSrc.set.call(this, v));
  } });
  win.Element.prototype.setAttribute = function (name, value) {
    if (this instanceof win.HTMLImageElement && String(name).toLowerCase() === 'src') {
      return showImage(this, value, v => setAttribute.call(this, name, v));
    }
    return setAttribute.call(this, name, value);
  };
  const removeAttribute = win.Element.prototype.removeAttribute;
  win.Element.prototype.removeAttribute = function (name) {
    if (this instanceof win.HTMLImageElement && String(name).toLowerCase() === 'src') wanted.delete(this);
    return removeAttribute.call(this, name);
  };

  // background-image: layers not fetched yet are drawn as none, and the value
  // is set again once they arrive if nothing has replaced it meanwhile.
  const requested = new WeakMap(); // style declaration -> last background-image asked for
  function rewrite(value, missing) {
    return value.replace(/url\(\s*(['"]?)(.*?)\1\s*\)/g, (whole, quote, raw) => {
      const url = mediaUrl(raw);
      if (!url) return whole;
      const blob = cached(url);
      if (blob) return `url("${blob}")`;
      missing?.push(url);
      return 'none';
    });
  }
  function showBackground(style, value, apply) {
    requested.set(style, value);
    if (typeof value !== 'string' || !value.includes('url(')) return apply(value);
    const missing = [];
    apply(rewrite(value, missing));
    if (!missing.length) return;
    Promise.allSettled(missing.map(load)).then(() => {
      if (requested.get(style) === value) apply(rewrite(value));
    });
  }
  let proto = win.document.documentElement.style;
  let background;
  while (proto && !(background = Object.getOwnPropertyDescriptor(proto, 'backgroundImage'))) proto = Object.getPrototypeOf(proto);
  if (background?.set) {
    Object.defineProperty(proto, 'backgroundImage', { ...background, set(value) {
      showBackground(this, value, v => background.set.call(this, v));
    } });
  }
  const setProperty = win.CSSStyleDeclaration.prototype.setProperty;
  win.CSSStyleDeclaration.prototype.setProperty = function (name, value, priority) {
    if (String(name).toLowerCase() !== 'background-image') return setProperty.call(this, name, value, priority);
    showBackground(this, value, v => setProperty.call(this, name, v, priority));
  };

  // Lock-screen and notification artwork: the OS fetches MediaMetadata
  // artwork itself, with no way to add a header, so a cover URL drew as a blank
  // square on the phone's lock screen. Hand it the blob instead, and set the
  // metadata again when the blob arrives if the track has not changed since.
  const session = win.navigator?.mediaSession;
  const Metadata = win.MediaMetadata;
  if (!session || typeof Metadata !== 'function') return;
  let proto2 = session;
  let metadata;
  while (proto2 && !(metadata = Object.getOwnPropertyDescriptor(proto2, 'metadata'))) proto2 = Object.getPrototypeOf(proto2);
  if (!metadata?.set) return;
  let latest = null;
  function withArtwork(meta, art) {
    return new Metadata({ title: meta.title, artist: meta.artist, album: meta.album, artwork: art });
  }
  Object.defineProperty(proto2, 'metadata', { ...metadata, set(value) {
    latest = value;
    const art = Array.from(value?.artwork ?? []);
    const urls = art.map(entry => mediaUrl(entry.src));
    if (!urls.some(Boolean)) return metadata.set.call(this, value);
    const swap = () => art.map((entry, i) => {
      const blob = urls[i] && cached(urls[i]);
      // An unknown type makes some platforms skip the image entirely.
      return urls[i] ? (blob ? { src: blob, sizes: entry.sizes } : null) : entry;
    }).filter(Boolean);
    metadata.set.call(this, withArtwork(value, swap()));
    const pending = urls.filter(url => url && !blobs.has(url));
    if (!pending.length) return;
    Promise.allSettled(pending.map(load)).then(() => {
      if (latest === value) metadata.set.call(this, withArtwork(value, swap()));
    });
  } });
}
