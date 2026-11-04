// Where a page sits in the phone layout's bottom tabs. Plain JS so the node
// tests can import it without a TypeScript step.

// The same five, in the same order, as the Android app's tab bar
// (SAMO_MOBILE_TABS in @samo/core). Music lives under Home, as it does there.
export const TAB_ROUTES = {
  home: '/',
  podcasts: '/podcasts',
  audiobooks: '/audiobooks',
  playlists: '/playlists',
  radio: '/radio',
};

// Pages reached from the menu rather than a tab, which still have nowhere
// further up to go back to.
const OTHER_ROOTS = ['/music', '/settings', '/favorites'];

function under(pathname, root) {
  return pathname === root || pathname.startsWith(root + '/');
}

// The tab to highlight: an episode is under Podcasts and a playlist's songs
// under Playlists, while albums, artists and tracks belong to Home. Settings
// and search are under none of them.
export function tabForPath(pathname = '/') {
  for (const id of ['podcasts', 'audiobooks', 'playlists', 'radio']) {
    if (under(pathname, TAB_ROUTES[id])) return id;
  }
  if (under(pathname, '/settings') || under(pathname, '/search')) return null;
  return 'home';
}

// A root has no back button: there is nothing above it.
export function isRootPath(pathname = '/') {
  return Object.values(TAB_ROUTES).includes(pathname) || OTHER_ROOTS.includes(pathname);
}

// Back goes back when there is somewhere in this tab's history to go: the
// first page of a session (opened from a link or a home-screen icon) has a
// router key of 'default', and leaving it would leave samo, so it goes up to
// its tab instead.
export function backTarget(pathname, locationKey) {
  if (locationKey && locationKey !== 'default') return -1;
  const tab = tabForPath(pathname);
  return TAB_ROUTES[tab ?? 'home'];
}
