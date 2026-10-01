import { test } from 'node:test';
import assert from 'node:assert/strict';
import { webcrypto } from 'node:crypto';
import { installRandomUUID } from '../client/compat.js';
import { artistNameBonus } from '../client/search-ranking.js';

test('HTTP browser without randomUUID produces unique RFC 4122 v4 identifiers', () => {
  const insecureCrypto = { getRandomValues: bytes => webcrypto.getRandomValues(bytes) };
  installRandomUUID(insecureCrypto);
  const values = Array.from({length:100}, () => insecureCrypto.randomUUID());
  assert.equal(new Set(values).size,100);
  for (const uuid of values) assert.match(uuid,/^[\da-f]{8}-[\da-f]{4}-4[\da-f]{3}-[89ab][\da-f]{3}-[\da-f]{12}$/);
});
test('secure browser native UUID implementation remains intact', () => {
  const native = () => 'native'; const crypto = { randomUUID:native };
  installRandomUUID(crypto); assert.equal(crypto.randomUUID,native);
});
test('Elvis artist result outranks album titles corroborated by the artist field', () => {
  // Current relevance scorer: prefix + specificity, exact + specificity, 35% secondary.
  const artist = 600 + Math.round(60 * 5 / 13) + artistNameBonus('Elvis Presley','elvis');
  const album = 1000 + 60 + (600 + Math.round(60 * 5 / 13)) * .35;
  assert.ok(artist > album);
  assert.equal(artistNameBonus('Elvis Presley','elvis presley'),700);
  assert.equal(artistNameBonus('Elvis Presley','presley'),0);
  assert.equal(artistNameBonus('Elvis Presley','love'),0);
  assert.equal(artistNameBonus('Elvis Presley',''),0);
});
