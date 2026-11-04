// Phone browsers, and the two things the desktop renderer assumes they do.
//
// iOS: every browser there is WebKit, and an iPad asks for the desktop site by
// calling itself a Mac, so the touch-point count is what gives it away.
export function isAppleTouch(nav) {
  return /iP(hone|od|ad)/.test(nav.userAgent || '') ||
    (/Macintosh/.test(nav.userAgent || '') && (nav.maxTouchPoints || 0) > 1);
}

// No drag and drop on a touch screen. Every song row and every cover is a
// drag source on the desktop (drop it on a playlist, reorder the queue), so
// each carries draggable="true". On a phone (iOS 15 and later, and Chrome on
// Android) a finger that rests a moment before a swipe then picks the cover
// up instead of scrolling, and the browser's drag auto-scroll creeps the list
// towards whichever edge the finger nears: a scroll that judders back and
// forth and barely moves. Elements are disarmed as the renderer arms them.
export function installNoTouchDrag(win) {
  const doc = win.document;
  const disarm = element => {
    if (element.getAttribute?.('draggable') === 'true') element.setAttribute('draggable', 'false');
  };
  const sweep = node => {
    if (node.nodeType !== 1) return;
    disarm(node);
    node.querySelectorAll('[draggable="true"]').forEach(disarm);
  };
  new win.MutationObserver(records => {
    for (const record of records) {
      if (record.type === 'attributes') disarm(record.target);
      else record.addedNodes.forEach(sweep);
    }
  }).observe(doc.documentElement, { attributeFilter: ['draggable'], attributes: true, childList: true, subtree: true });
  sweep(doc.documentElement);
  // Anything that still starts a drag (an image, a link) does not.
  doc.addEventListener('dragstart', event => event.preventDefault(), { capture: true });
}

// Long press opens the context menu. Safari on iOS never fires contextmenu
// for a held finger, which left the renderer's menus (add to queue, play next,
// go to album) out of reach of anything but the ⋯ button. Chrome on Android
// does fire one; there this is a later fallback that its own cancels. This fires one where the finger is
// held, and eats the click that would otherwise follow when it lifts, so the
// row under it does not also start playing.
export function installLongPress(win, { delay = 500, slop = 10 } = {}) {
  const doc = win.document;
  let timer = null;
  let start = null;
  let suppressUntil = 0;
  const synthetic = new WeakSet();

  function cancel() {
    if (timer) win.clearTimeout(timer);
    timer = null;
    start = null;
  }

  doc.addEventListener('touchstart', event => {
    cancel();
    if (event.touches.length !== 1) return;
    const touch = event.touches[0];
    const target = event.target;
    // A text field keeps the platform's own press-and-hold (the magnifier,
    // select and paste).
    if (target?.closest?.('input, textarea, [contenteditable="true"]')) return;
    start = { target, x: touch.clientX, y: touch.clientY };
    timer = win.setTimeout(() => {
      const { target: held, x, y } = start;
      cancel();
      const menu = new win.MouseEvent('contextmenu', {
        bubbles: true, cancelable: true, view: win, button: 2, buttons: 0,
        clientX: x, clientY: y, screenX: x, screenY: y,
      });
      synthetic.add(menu);
      // Only swallow the lift when something actually opened a menu.
      if (!held.dispatchEvent(menu)) suppressUntil = Date.now() + 800;
    }, delay);
  }, { passive: true, capture: true });

  // Should the browser fire a contextmenu of its own for the press, that one
  // is the menu: never open a second on top of it.
  doc.addEventListener('contextmenu', event => {
    if (!synthetic.has(event)) cancel();
  }, { capture: true });

  doc.addEventListener('touchmove', event => {
    if (!start) return;
    const touch = event.touches[0];
    if (Math.abs(touch.clientX - start.x) > slop || Math.abs(touch.clientY - start.y) > slop) cancel();
  }, { passive: true, capture: true });
  doc.addEventListener('touchend', cancel, { passive: true, capture: true });
  doc.addEventListener('touchcancel', cancel, { passive: true, capture: true });
  // The page scrolling under a resting finger is not a press.
  doc.addEventListener('scroll', cancel, { passive: true, capture: true });

  doc.addEventListener('click', event => {
    if (Date.now() > suppressUntil) return;
    suppressUntil = 0;
    event.preventDefault();
    event.stopPropagation();
  }, { capture: true });
}
