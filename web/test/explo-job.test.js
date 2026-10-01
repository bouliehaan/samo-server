import { test } from 'node:test';
import assert from 'node:assert/strict';
import { activeJob, jobLabel, jobStatus, openJob, providerName } from '../src/ui/explo_job.js';
test('queued is distinct from provider transfer and provider is visible', () => {
  assert.equal(providerName('youtube'), 'YouTube (yt-dlp)');
  assert.match(jobStatus({state:'queued',provider:'slskd'}), /Waiting for a download slot · Soulseek/);
  assert.equal(jobStatus({state:'downloading',provider:'youtube',message:'YouTube (yt-dlp): converting audio…'}), 'YouTube (yt-dlp): converting audio…');
  assert.equal(activeJob({state:'failed'}), false);
  assert.equal(activeJob({state:'downloading'}), true);
});
test('actionable provider error survives into failed UI state', () => {
  const message = 'Soulseek: peer rejected the transfer. YouTube (yt-dlp): time limit reached.';
  assert.equal(jobStatus({state:'failed',message}), message);
});
test('a staged download is followed until samo has it in the library', () => {
  const staged = {state:'staged', message:'Downloaded to Explo staging.'};
  assert.equal(jobLabel(staged), 'IDENTIFYING…');
  assert.equal(openJob(staged), true);
  assert.equal(activeJob(staged), false);
  const identifying = {...staged, library:{state:'identifying', message:'Identified. Fetching cover art before adding it to your library.'}};
  assert.equal(jobStatus(identifying), 'Identified. Fetching cover art before adding it to your library.');
  const kept = {...staged, library:{state:'in-library', message:'Added to your library.', libraryAlbumId:'album-1'}};
  assert.equal(jobLabel(kept), 'IN LIBRARY');
  assert.equal(openJob(kept), false);
  assert.equal(jobStatus(kept), 'Added to your library.');
  const review = {...staged, library:{state:'needs-review', message:'The downloaded audio is "X" by Y, not the song you asked for.'}};
  assert.equal(jobLabel(review), 'NEEDS REVIEW');
  assert.equal(openJob(review), false);
  assert.equal(jobLabel(undefined), 'ADD SONG');
  assert.equal(jobLabel({state:'failed'}), 'RETRY');
});
