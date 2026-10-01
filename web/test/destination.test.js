import { test } from 'node:test';
import assert from 'node:assert/strict';
import { loginDestination } from '../src/ui/destination.js';
const origin = 'https://samo.example';
test('role defaults and dashboard boundaries', () => {
  assert.equal(loginDestination({role:'admin'}, null, origin), '/app');
  assert.equal(loginDestination({role:'user'}, null, origin), '/listen/');
  assert.equal(loginDestination({role:'user'}, '/app#settings', origin), '/listen/');
  assert.equal(loginDestination({role:'admin'}, '/app#users', origin), '/app#users');
});
test('preserve listener deep links and device pairing for both roles', () => {
  for (const role of ['admin','user']) {
    for (const next of ['/listen/#/library/albums/123', '/pair?code=123456']) {
      assert.equal(loginDestination({role}, next, origin), next);
    }
  }
});
test('reject off-site destinations and login loops', () => {
  for (const next of ['//evil.example', '/\\evil.example', 'https://evil.example/app', '/login', '/setup', '/listen/assets/file.js', 'javascript:alert(1)']) {
    assert.equal(loginDestination({role:'user'}, next, origin), '/listen/');
  }
});
