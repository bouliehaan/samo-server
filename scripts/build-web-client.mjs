// Build the actual desktop renderer in an isolated staging directory. Its
// dependencies must already be installed; the source checkout is never edited.
import { cp, copyFile, mkdir, mkdtemp, readFile, rm, symlink, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { execFileSync } from 'node:child_process';
import { patchWebClient } from './web-client-patches.mjs';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const source = resolve(process.argv[2] || join(root, '../samo'));
const stage = await mkdtemp(join(tmpdir(), 'samo-web-client-'));
try {
  for (const path of ['src', 'packages', 'assets', 'resources', 'build']) {
    await cp(join(source, path), join(stage, path), { recursive: true, filter: p => !p.split('/').includes('node_modules') });
  }
  for (const file of ['package.json', 'postcss.config.cjs', 'vite.react-plugin.ts', 'vite.samo-core-aliases.ts']) {
    await copyFile(join(source, file), join(stage, file));
  }
  await symlink(join(source, 'node_modules'), join(stage, 'node_modules'), 'dir');
  await copyFile(join(root, 'web/client/bootstrap.js'), join(stage, 'src/renderer/server-bootstrap.js'));
  await copyFile(join(root, 'web/client/host.css'), join(stage, 'src/renderer/server-host.css'));
  for (const name of ['compat', 'search-ranking']) await copyFile(join(root, `web/client/${name}.js`), join(stage, `src/renderer/server-${name}.js`));
  await patchWebClient(stage);
  let html = await readFile(join(stage, 'src/renderer/index.html'), 'utf8');
  html = html.replace(/<meta\s+http-equiv="Content-Security-Policy"[\s\S]*?\/>/, '')
    .replace(/<% if \(web\) \{ %>[\s\S]*?<% } %>/, '')
    .replace('src="main.tsx"', 'src="server-bootstrap.js"');
  await writeFile(join(stage, 'src/renderer/index.html'), html);
  const config = `import { defineConfig } from 'vite';
import path from 'node:path';
import { createReactPlugin } from './vite.react-plugin';
import { samoCoreAliases } from './vite.samo-core-aliases';
export default defineConfig({
 root: path.resolve(__dirname, 'src/renderer'), base: '/listen/',
 build: { outDir: ${JSON.stringify(join(stage, 'output'))}, emptyOutDir: true, sourcemap: false },
 css: { modules: { generateScopedName: 'fs-[name]-[local]', localsConvention: 'camelCase' } },
 plugins: [createReactPlugin()],
 resolve: { alias: [
 {find: '/@/i18n', replacement: path.resolve(__dirname, 'src/i18n')},
 {find: '/@/remote', replacement: path.resolve(__dirname, 'src/remote')},
 {find: '/@/renderer', replacement: path.resolve(__dirname, 'src/renderer')},
 {find: '/@/shared', replacement: path.resolve(__dirname, 'src/shared')}, ...samoCoreAliases] }
});`;
  await writeFile(join(stage, 'server.vite.config.ts'), config);
  execFileSync(process.execPath, [join(source, 'node_modules/vite/bin/vite.js'), 'build', '--config', join(stage, 'server.vite.config.ts')], { cwd: stage, stdio: 'inherit' });
  const output = join(root, 'internal/api/web-client');
  // Replace generated files only after a successful build.
  await rm(output, { recursive: true, force: true });
  await mkdir(output, { recursive: true });
  await cp(join(stage, 'output'), output, { recursive: true });
  await copyFile(join(source, 'LICENSE'), join(output, 'LICENSE'));
  const revision = execFileSync('git', ['rev-parse', 'HEAD'], { cwd: source, encoding: 'utf8' }).trim();
  const dirty = Boolean(execFileSync('git', ['-c', 'core.fsmonitor=false', 'status', '--porcelain', '--untracked-files=no'], { cwd: source, encoding: 'utf8' }).trim());
  await writeFile(join(output, 'source.json'), JSON.stringify({ repository: 'https://github.com/bouliehaan/samo', revision, dirty }, null, 2) + '\n');
} finally { await rm(stage, { recursive: true, force: true }); }
