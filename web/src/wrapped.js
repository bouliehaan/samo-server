// Entry point for /wrapped: a listener's year in samo, as a run of full-screen
// slides. Tap the right of the screen (or press →) for the next, the left for
// the one before; press and hold to pause. Every number comes from
// GET /api/v1/wrapped (internal/wrapped), which reads the year from the
// listener's Last.fm account, or from samo's own history if they turned it on.
import './wrapped.css';

const root = document.getElementById('wrapped');
const token = localStorage.getItem('samo-token');
const params = new URLSearchParams(location.search);
const year = Number(params.get('year')) || new Date().getFullYear();
const SLIDE_MS = 7000;
const reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches;

if (!token) {
  location.replace('/login?next=' + encodeURIComponent(location.pathname + location.search));
}

async function api(path) {
  const response = await fetch(path, { headers: { Authorization: 'Bearer ' + token } });
  if (response.status === 401) {
    localStorage.removeItem('samo-token');
    location.replace('/login?next=' + encodeURIComponent(location.pathname + location.search));
    throw new Error('signed out');
  }
  const body = await response.json().catch(() => ({}));
  return { body, status: response.status };
}

// ---- small DOM helpers ------------------------------------------------------

function el(tag, className, ...children) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  for (const child of children) {
    if (child === null || child === undefined || child === false) continue;
    node.append(child instanceof Node ? child : document.createTextNode(String(child)));
  }
  return node;
}

const number = value => Number(value || 0).toLocaleString();
const months = ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December'];

function longDate(iso) {
  const date = new Date(iso.length === 10 ? iso + 'T12:00:00' : iso);
  return date.toLocaleDateString(undefined, { day: 'numeric', month: 'long' });
}

function hourLabel(hour) {
  const suffix = hour < 12 ? 'am' : 'pm';
  return `${hour % 12 || 12}${suffix}`;
}

// Covers: samo's own when the item is in the library (fetched with the
// bearer, since the cover routes need one), else the art Last.fm sent.
const coverCache = new Map();
// In order of preference: an artist without a photo of their own shows the
// cover of an album of theirs that was played.
function coverURLs(item, kind) {
  const urls = [];
  if (kind === 'audiobook' && item.trackId) urls.push(`/api/v1/audiobooks/${encodeURIComponent(item.trackId)}/cover`);
  if (kind === 'podcast' && item.albumId) urls.push(`/api/v1/podcasts/shows/${encodeURIComponent(item.albumId)}/cover`);
  if (kind === 'artist' && item.artistId) urls.push(`/api/v1/music/artists/${encodeURIComponent(item.artistId)}/cover?width=600`);
  if (kind !== 'audiobook' && kind !== 'podcast' && item.albumId) urls.push(`/api/v1/music/albums/${encodeURIComponent(item.albumId)}/cover?width=600`);
  return urls;
}

function fetchCover(url) {
  if (!coverCache.has(url)) {
    coverCache.set(url, fetch(url, { headers: { Authorization: 'Bearer ' + token } })
      .then(response => (response.ok ? response.blob() : Promise.reject(new Error(String(response.status)))))
      .then(blob => URL.createObjectURL(blob))
      .catch(() => null));
  }
  return coverCache.get(url);
}

function cover(item, kind, className = 'wrapped-cover') {
  const box = el('div', className + (kind === 'artist' ? ' is-round' : ''));
  const initial = el('span', 'wrapped-cover-initial', (item.name || item.title || '?').trim().charAt(0).toUpperCase());
  box.append(initial);
  const show = src => {
    const img = el('img');
    img.alt = '';
    img.decoding = 'async';
    img.onload = () => { initial.remove(); box.classList.add('has-image'); };
    img.onerror = () => img.remove();
    img.src = src;
    box.prepend(img);
  };
  (async () => {
    for (const url of coverURLs(item, kind)) {
      const blob = await fetchCover(url);
      if (blob) return show(blob);
    }
    if (item.image) show(item.image);
  })();
  return box;
}

// A number that counts up as its slide appears.
function countUp(value, suffix = '') {
  const node = el('span', 'wrapped-count', number(value) + suffix);
  node.dataset.value = String(value || 0);
  node.dataset.suffix = suffix;
  return node;
}

function animateCounts(slide) {
  if (reduceMotion) return;
  for (const node of slide.querySelectorAll('.wrapped-count')) {
    const target = Number(node.dataset.value);
    const suffix = node.dataset.suffix || '';
    const started = performance.now();
    const tick = now => {
      const t = Math.min(1, (now - started) / 1400);
      const eased = 1 - Math.pow(1 - t, 3);
      node.textContent = number(Math.round(target * eased)) + suffix;
      if (t < 1) requestAnimationFrame(tick);
    };
    requestAnimationFrame(tick);
  }
}

// ---- slides -----------------------------------------------------------------

function slide(theme, ...children) {
  return el('section', `wrapped-slide theme-${theme}`, el('div', 'wrapped-slide-body', ...children));
}

function rankList(items, kind) {
  const list = el('ol', 'wrapped-rank');
  items.forEach((item, i) => {
    list.append(el('li', 'wrapped-rank-row',
      el('span', 'wrapped-rank-n', i + 1),
      cover(item, kind, 'wrapped-cover is-small'),
      el('span', 'wrapped-rank-text',
        el('span', 'wrapped-rank-name', item.name),
        item.artist && kind !== 'artist' ? el('span', 'wrapped-rank-sub', item.artist) : null),
      el('span', 'wrapped-rank-plays', kind === 'audiobook' ? `${number(item.minutes)} min` : `${number(item.plays)}×`)));
  });
  return list;
}

function bars(values, labels, highlight) {
  const max = Math.max(1, ...values);
  const chart = el('div', 'wrapped-bars');
  values.forEach((value, i) => {
    const bar = el('div', 'wrapped-bar' + (i === highlight ? ' is-top' : ''));
    bar.style.setProperty('--h', `${Math.max(3, (value / max) * 100)}%`);
    bar.title = `${labels[i]}: ${number(value)}`;
    chart.append(el('div', 'wrapped-bar-col', bar, el('span', 'wrapped-bar-label', labels[i])));
  });
  return chart;
}

function buildSlides(report, me) {
  const music = report.music;
  const slides = [];
  const name = me?.displayName || me?.username || '';
  const source = report.source === 'lastfm'
    ? `From your Last.fm, ${report.lastfmUser}`
    : `From samo, since ${longDate(report.from)}`;

  slides.push(slide('intro',
    el('p', 'wrapped-kicker', 'samo Wrapped'),
    el('h1', 'wrapped-display', `${name ? name + ', your' : 'Your'} ${report.year},`, el('br'), 'wrapped.'),
    el('p', 'wrapped-note', report.complete ? source : `${source} · the year so far`)));

  if (music) {
    const hours = Math.round(music.minutes / 60);
    slides.push(slide('minutes',
      el('p', 'wrapped-kicker', 'This year you listened to'),
      el('p', 'wrapped-huge', music.minutesEstimated ? '≈ ' : '', countUp(music.minutes)),
      el('p', 'wrapped-lead', 'minutes of music'),
      el('p', 'wrapped-note', `That's ${number(hours)} hours, across ${number(music.days)} days, ${number(music.tracks)} songs and ${number(music.artists)} artists.`)));

    const topArtist = music.topArtists[0];
    if (topArtist) {
      slides.push(slide('artist',
        el('p', 'wrapped-kicker', 'Your top artist'),
        cover(topArtist, 'artist', 'wrapped-cover is-hero'),
        el('h2', 'wrapped-title', topArtist.name),
        el('p', 'wrapped-note', `${number(topArtist.plays)} plays · ${number(topArtist.minutes)} minutes`)));
      if (music.topArtists.length > 1) {
        slides.push(slide('artists', el('p', 'wrapped-kicker', 'Your top artists'), rankList(music.topArtists, 'artist')));
      }
    }

    const topTrack = music.topTracks[0];
    if (topTrack) {
      slides.push(slide('song',
        el('p', 'wrapped-kicker', 'Your song of the year'),
        cover(topTrack, 'track', 'wrapped-cover is-hero'),
        el('h2', 'wrapped-title', topTrack.name),
        el('p', 'wrapped-lead', topTrack.artist),
        el('p', 'wrapped-note', `You played it ${number(topTrack.plays)} times.`)));
      if (music.topTracks.length > 1) {
        slides.push(slide('songs', el('p', 'wrapped-kicker', 'Your top songs'), rankList(music.topTracks, 'track')));
      }
    }
    if (music.topAlbums.length) {
      slides.push(slide('albums', el('p', 'wrapped-kicker', 'Your top albums'), rankList(music.topAlbums, 'album')));
    }

    slides.push(slide('clock',
      el('p', 'wrapped-kicker', 'When you listened'),
      el('h2', 'wrapped-title', `You're ${/^[aeiou]/i.test(music.clock.name) ? 'an' : 'a'} ${music.clock.name.toLowerCase()}.`),
      el('p', 'wrapped-lead', music.clock.blurb),
      bars(music.hours, music.hours.map((_, h) => (h % 6 === 0 ? hourLabel(h) : '')), music.clock.peakHour),
      el('p', 'wrapped-note', `Your peak hour was ${hourLabel(music.clock.peakHour)}. Your biggest day was ${longDate(music.busiestDay.date)}, with ${number(music.busiestDay.listens)} songs.`)));

    if (music.onRepeat && music.onRepeat.plays) {
      slides.push(slide('repeat',
        el('p', 'wrapped-kicker', 'On repeat'),
        cover({ ...music.onRepeat, name: music.onRepeat.title }, 'track', 'wrapped-cover is-hero'),
        el('h2', 'wrapped-title', music.onRepeat.title),
        el('p', 'wrapped-lead', `${number(music.onRepeat.plays)} plays on ${longDate(music.onRepeat.date)}.`),
        el('p', 'wrapped-note', 'Some days only one song will do.')));
    }

    slides.push(slide('months',
      el('p', 'wrapped-kicker', 'Your year in music'),
      el('h2', 'wrapped-title', `${months[music.topMonth - 1]} was your month.`),
      bars(music.months, months.map(m => m.charAt(0)), music.topMonth - 1),
      el('p', 'wrapped-note', music.longestStreak > 1
        ? `Your longest streak: ${number(music.longestStreak)} days in a row with music.`
        : 'Minutes listened, month by month.')));
  }

  const books = report.audiobooks;
  if (books) {
    slides.push(slide('books',
      el('p', 'wrapped-kicker', 'Between the songs'),
      el('p', 'wrapped-huge', countUp(Math.round(books.minutes / 60))),
      el('p', 'wrapped-lead', `hours of audiobooks, across ${number(books.books)} ${books.books === 1 ? 'book' : 'books'}`),
      books.finished ? el('p', 'wrapped-note', `You finished ${number(books.finished)}.`) : null,
      books.top.length ? rankList(books.top.slice(0, 3), 'audiobook') : null));
  }
  const pods = report.podcasts;
  if (pods) {
    slides.push(slide('podcasts',
      el('p', 'wrapped-kicker', 'Your podcasts'),
      el('p', 'wrapped-huge', countUp(pods.episodes)),
      el('p', 'wrapped-lead', `${pods.episodes === 1 ? 'episode' : 'episodes'} from ${number(pods.shows)} ${pods.shows === 1 ? 'show' : 'shows'}`),
      pods.top.length ? rankList(pods.top.slice(0, 3), 'podcast') : null));
  }

  slides.push(slide('tier',
    el('p', 'wrapped-kicker', 'So, the verdict'),
    el('p', 'wrapped-lead', 'Are you a baller? A shot caller? Or a not-at-aller?'),
    el('h1', 'wrapped-display wrapped-tier', report.tier.name + '.'),
    el('p', 'wrapped-note', report.tier.blurb)));

  slides.push(summarySlide(report, name));
  return slides;
}

function summarySlide(report, name) {
  const music = report.music;
  const card = el('div', 'wrapped-card');
  card.append(el('p', 'wrapped-card-head', `${name ? name + "'s " : ''}${report.year} on samo`));
  if (music) {
    const columns = el('div', 'wrapped-card-cols');
    const column = (title, items) => el('div', 'wrapped-card-col', el('p', 'wrapped-card-label', title),
      el('ol', 'wrapped-card-list', ...items.slice(0, 5).map(item => el('li', null, item.name))));
    columns.append(column('Top artists', music.topArtists), column('Top songs', music.topTracks));
    card.append(columns);
    card.append(el('div', 'wrapped-card-stats',
      el('div', null, el('p', 'wrapped-card-label', 'Minutes'), el('p', 'wrapped-card-value', (music.minutesEstimated ? '≈ ' : '') + number(music.minutes))),
      el('div', null, el('p', 'wrapped-card-label', 'Verdict'), el('p', 'wrapped-card-value', report.tier.name))));
  } else {
    card.append(el('p', 'wrapped-card-value', report.tier.name));
  }
  const actions = el('div', 'wrapped-actions');
  if (navigator.share && music) {
    const share = el('button', 'wrapped-button', 'Share');
    share.type = 'button';
    share.addEventListener('click', event => {
      event.stopPropagation();
      const text = [
        `My ${report.year} on samo:`,
        `Top artists: ${music.topArtists.map(a => a.name).join(', ')}`,
        `Top songs: ${music.topTracks.map(t => `${t.name} (${t.artist})`).join(', ')}`,
        `${number(music.minutes)} minutes. Verdict: ${report.tier.name}.`,
      ].join('\n');
      navigator.share({ title: `samo Wrapped ${report.year}`, text }).catch(() => {});
    });
    actions.append(share);
  }
  const back = el('a', 'wrapped-button is-quiet', 'Back to samo');
  back.href = '/listen/';
  actions.append(back);
  return slide('summary', el('p', 'wrapped-kicker', 'samo Wrapped'), card, actions);
}

// ---- the story player ---------------------------------------------------------

function play(slides) {
  root.replaceChildren();
  const progress = el('div', 'wrapped-progress');
  const segments = slides.map(() => {
    const segment = el('span', 'wrapped-progress-seg', el('span', 'wrapped-progress-fill'));
    progress.append(segment);
    return segment;
  });
  const close = el('a', 'wrapped-close', '×');
  close.href = '/listen/';
  close.setAttribute('aria-label', 'Close');
  const stage = el('div', 'wrapped-stage', ...slides);
  root.append(stage, progress, close);

  let index = -1;
  let timer = null;
  let remaining = SLIDE_MS;
  let startedAt = 0;
  let paused = false;

  const schedule = ms => {
    clearTimeout(timer);
    remaining = ms;
    startedAt = performance.now();
    if (index < slides.length - 1) timer = setTimeout(() => show(index + 1), ms);
  };

  function show(next) {
    if (next < 0 || next >= slides.length) return;
    index = next;
    slides.forEach((s, i) => s.classList.toggle('is-active', i === index));
    segments.forEach((segment, i) => {
      segment.classList.toggle('is-done', i < index);
      segment.classList.toggle('is-active', i === index);
      const fill = segment.firstChild;
      fill.style.animation = 'none';
      void fill.offsetWidth; // restart the fill animation
      fill.style.animation = '';
      fill.style.animationDuration = `${SLIDE_MS}ms`;
    });
    animateCounts(slides[index]);
    paused = false;
    root.classList.remove('is-paused');
    schedule(SLIDE_MS);
  }

  const pause = () => {
    if (paused) return;
    paused = true;
    clearTimeout(timer);
    remaining -= performance.now() - startedAt;
    root.classList.add('is-paused');
  };
  const resume = () => {
    if (!paused) return;
    paused = false;
    root.classList.remove('is-paused');
    schedule(Math.max(0, remaining));
  };

  // A tap moves on or back; a held press pauses until it lets go.
  let pressAt = 0;
  let holdTimer = null;
  stage.addEventListener('pointerdown', event => {
    if (event.target.closest('a, button')) return;
    pressAt = performance.now();
    holdTimer = setTimeout(pause, 220);
  });
  stage.addEventListener('pointerup', event => {
    clearTimeout(holdTimer);
    if (event.target.closest('a, button')) return;
    if (paused && performance.now() - pressAt >= 220) {
      resume();
      return;
    }
    const left = event.clientX < window.innerWidth / 3;
    show(left ? index - 1 : index + 1);
  });
  stage.addEventListener('pointercancel', () => { clearTimeout(holdTimer); resume(); });
  window.addEventListener('keydown', event => {
    if (event.key === 'ArrowRight' || event.key === ' ') { event.preventDefault(); show(index + 1); }
    if (event.key === 'ArrowLeft') show(index - 1);
    if (event.key === 'Escape') location.assign('/listen/');
  });
  document.addEventListener('visibilitychange', () => (document.hidden ? pause() : resume()));

  show(0);
}

// ---- screens before the story ---------------------------------------------------

function message(title, text, ...actions) {
  root.replaceChildren(el('section', 'wrapped-slide theme-intro is-active is-static',
    el('div', 'wrapped-slide-body',
      el('p', 'wrapped-kicker', `samo Wrapped ${year}`),
      el('h2', 'wrapped-title', title),
      el('p', 'wrapped-lead', text),
      el('div', 'wrapped-actions', ...actions))));
}

function link(text, href, quiet = false) {
  const a = el('a', 'wrapped-button' + (quiet ? ' is-quiet' : ''), text);
  a.href = href;
  return a;
}

function building(progress) {
  const pages = progress?.totalPages || 0;
  const done = progress?.pages || 0;
  const bar = el('div', 'wrapped-meter', el('span', 'wrapped-meter-fill'));
  bar.firstChild.style.width = pages ? `${Math.round((done / pages) * 100)}%` : '8%';
  root.replaceChildren(el('section', 'wrapped-slide theme-intro is-active is-static',
    el('div', 'wrapped-slide-body',
      el('p', 'wrapped-kicker', `samo Wrapped ${year}`),
      el('h2', 'wrapped-title', 'Reading your year from Last.fm…'),
      el('p', 'wrapped-lead', pages ? `${number(done * 200)} of about ${number(pages * 200)} listens` : 'Starting…'),
      bar,
      el('p', 'wrapped-note', 'A big year takes a minute. It only happens once; after that it opens straight away.'))));
}

async function load(refresh = false) {
  try {
    const [{ body, status }, me] = await Promise.all([
      api(`/api/v1/wrapped?year=${year}${refresh ? '&refresh=1' : ''}`),
      api('/api/v1/users/me').then(r => r.body).catch(() => null),
    ]);
    if (status === 202) {
      building(body.building);
      setTimeout(() => load(), 1500);
      return;
    }
    if (status === 404 && body.code === 'no-source') {
      message('Nothing to wrap yet.',
        'samo builds your Wrapped from your Last.fm account. Link it in Settings, under Scrobbling. Or turn on samo’s own listening history there, and samo will keep track for you.',
        link('Open Settings', '/listen/#/settings'), link('Back to samo', '/listen/', true));
      return;
    }
    if (status !== 200) {
      message('Wrapped didn’t load.', body.error || 'Something went wrong. Try again in a minute.',
        link('Try again', location.href), link('Back to samo', '/listen/', true));
      return;
    }
    if (!body.music && !body.audiobooks && !body.podcasts) {
      message('A quiet year so far.',
        body.source === 'samo'
          ? 'samo hasn’t heard you play anything since you turned on its listening history. Play some music and check back.'
          : `Your Last.fm account (${body.lastfmUser}) has no listens in ${year} yet.`,
        link('Back to samo', '/listen/'));
      return;
    }
    play(buildSlides(body, me));
  } catch (error) {
    if (error.message !== 'signed out') {
      message('Wrapped didn’t load.', 'samo couldn’t be reached. Check your connection and try again.',
        link('Try again', location.href), link('Back to samo', '/listen/', true));
    }
  }
}

if (token) load(params.get('refresh') === '1');
