// Hosted-browser adaptations are applied only to the temporary renderer copy.
// Fail on upstream drift rather than silently shipping an unapplied fix.
import { readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
export async function patchWebClient(stage) {
  async function replace(path, before, after) {
    const file = join(stage, path);
    const source = await readFile(file, 'utf8');
    if (source.split(before).length !== 2) throw new Error(`Browser patch no longer matches: ${path}`);
    await writeFile(file, source.replace(before, after));
  }
  const engine = 'src/renderer/features/player/audio-player/engine/web-player-engine.tsx';
  // The idle second deck must never retry its placeholder and pause the live deck.
  await replace(engine, 'if (!player || !(target instanceof HTMLAudioElement)) {',
    'if (!sourceRef.current || !player || !(target instanceof HTMLAudioElement)) {');
  await replace(engine, '? player1Ref.current?.seekTo(seekTo)\n                : player2Ref.current?.seekTo(seekTo);',
    "? player1Ref.current?.seekTo(seekTo, 'seconds')\n                : player2Ref.current?.seekTo(seekTo, 'seconds');");
  // Whole-file podcast streams retain container headers and support native byte
  // ranges in both directions. Resume is performed by the browser player.
  await replace('src/renderer/api/samo/samo-long-form.ts',
    '    const resume = Math.max(0, Math.floor(progressSeconds ?? 0));\n', '');
  await replace('src/renderer/api/samo/samo-long-form.ts',
    '        ...(resume > 0 ? { offsetSeconds: resume } : {}),\n', '');
  // The S menu carries the hosted player's own items from bootstrap.js:
  // Server admin (admins only) and Sign out, after the menu's own.
  await replace('src/renderer/features/titlebar/components/app-menu.tsx',
    "        {\n            condition: isElectron(),\n            id: 'quit',",
    "        ...((window as any).SAMO_HOST_MENU ?? []).map((entry: any) => ({ ...entry, type: 'item' as const })),\n        {\n            condition: isElectron(),\n            id: 'quit',");
  // This is a single-server player over the complete library, with no folder setup.
  await writeFile(join(stage, 'src/renderer/features/sidebar/components/server-selector.tsx'),
    'export const ServerSelector = () => null;\n');
  const search = 'src/renderer/features/search/hooks/use-unified-search.ts';
  await replace(search, "const ENTITY_TYPE_BUMP = 50;", "const ENTITY_TYPE_BUMP = 50;\nimport { artistNameBonus } from '/@/renderer/server-search-ranking.js';");
  await replace(search, "scoreCandidate(artist.name, [], ctx.needle, ctx.tokens) +\n                entityBumpFor('artists', ctx)",
    "scoreCandidate(artist.name, [], ctx.needle, ctx.tokens) +\n                artistNameBonus(artist.name, ctx.needle) + entityBumpFor('artists', ctx)");
  await replace(search, '.sort((a, b) => b.score - a.score || a.artist.name.localeCompare(b.artist.name));',
    '.sort((a, b) => b.score - a.score || (b.artist.albumCount ?? 0) - (a.artist.albumCount ?? 0) || a.artist.name.localeCompare(b.artist.name));');
}
