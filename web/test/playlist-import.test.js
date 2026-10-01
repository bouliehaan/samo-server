import { test } from 'node:test';
import assert from 'node:assert/strict';
import { importCounts, importOpen, playlistImportPanel } from '../src/ui/playlist_import.js';

const view = {
  playlistId: 'playlist_1', title: 'CHET BAKER BALLADS', sourceUrl: 'https://music.youtube.com/playlist?list=PL1', unavailable: 2,
  tracks: [
    {position: 0, title: 'Over the Rainbow', artist: 'Chet Baker', state: 'in-library'},
    {position: 1, title: 'Almost Blue', artist: 'Chet Baker', album: "Let's Get Lost", durationMs: 300000, state: 'downloading', message: 'Waiting for an available download slot.'},
    {position: 2, title: 'My <Funny> Valentine', artist: 'Chet Baker', state: 'failed', message: 'YouTube (yt-dlp): found nothing for this song.'},
    {position: 3, title: 'Gone', artist: 'Chet Baker', state: 'unavailable'},
    {position: 4, title: 'Wrong Song', artist: 'Chet Baker', state: 'needs-review'},
  ],
};

test('counts and openness follow the tracks still moving', () => {
  assert.deepEqual(importCounts(view), {total: 5, library: 1, waiting: 0, downloading: 1, identifying: 0, review: 1, failed: 2});
  assert.equal(importOpen(view), true);
  assert.equal(importOpen({...view, tracks: view.tracks.filter((t) => t.state !== 'downloading')}), false);
});

test('the panel lists only songs not yet in the playlist, escaped, with retry for admins', () => {
  const html = playlistImportPanel(view, true);
  assert.match(html, /1 of 5 in library · 1 downloading · 1 need review · 2 failed · 2 not playable on YouTube Music/);
  assert.doesNotMatch(html, /Over the Rainbow/);
  assert.match(html, /My &lt;Funny&gt; Valentine/);
  assert.match(html, /RETRY 2 SONGS/);
  assert.match(html, /data-playlist-id="playlist_1"/);
  assert.doesNotMatch(playlistImportPanel(view, false), /RETRY/);
});
