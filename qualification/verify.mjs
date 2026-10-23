import {readFileSync, lstatSync, readdirSync} from 'node:fs';
import {createHash} from 'node:crypto';
import {fileURLToPath} from 'node:url';
import {resolve, sep} from 'node:path';

const root = resolve(fileURLToPath(new URL('../', import.meta.url)));
const payload = JSON.parse(readFileSync(new URL('./payload.json', import.meta.url), 'utf8'));
const fail = message => { throw Error('Public qualification refused: ' + message); };
function regular(rel) {
  if (typeof rel !== 'string' || !rel || rel.includes('\\') || rel.split('/').some(p => !p || p === '.' || p === '..')) fail('invalid input path');
  let path = root;
  for (const part of rel.split('/')) {
    path = resolve(path, part);
    if (!path.startsWith(root + sep) || lstatSync(path).isSymbolicLink()) fail('linked or escaping input');
  }
  if (!lstatSync(path).isFile()) fail('non-regular input');
  return path;
}
const runtime = new Set(['qualification/web/node_modules', 'qualification/web/playwright-report', 'qualification/web/test-results']);
function inventory(rel) {
  const dir = resolve(root, rel);
  if (!dir.startsWith(root + sep) || lstatSync(dir).isSymbolicLink() || !lstatSync(dir).isDirectory()) fail('invalid input directory');
  const files = [];
  for (const entry of readdirSync(dir, {withFileTypes:true})) {
    const child = rel + '/' + entry.name;
    if (runtime.has(child)) continue;
    if (entry.isSymbolicLink() || (!entry.isDirectory() && !entry.isFile())) fail('non-regular tree entry');
    if (entry.isDirectory()) files.push(...inventory(child));
    else files.push(child);
  }
  return files.sort();
}
const equal = (a,b) => JSON.stringify(a) === JSON.stringify(b);
if (payload.schema_version !== 1 || payload.source_exact !== true || payload.protected_or_personal_data !== false
    || !/^\d+\.\d+\.\d+$/.test(payload.curriculum_version) || !/^[a-f0-9]{40}$/.test(payload.source_ref) || !/^[a-f0-9]{64}$/.test(payload.content_digest)) fail('non-exact source metadata');
const fixtures = [
  'qualification/curriculum/missions/git-manual-pr/mission.json',
  'qualification/curriculum/missions/euler-access-ssh/mission.json',
  'qualification/passport_system/agent_contract.json',
  'qualification/web/dist/data/routes.json',
];
const groups = ['assets', 'test_files', 'fixtures', 'harness'];
for (const group of groups) {
  const values = payload[group];
  if (!values || typeof values !== 'object' || Array.isArray(values) || !Object.keys(values).length) fail('missing input group');
  for (const [rel, hash] of Object.entries(values)) {
    const prefix = group === 'assets' ? 'internal/webdist/bundle/' : group === 'test_files' ? 'qualification/web/tests/' : 'qualification/';
    if (!rel.startsWith(prefix) || !/^[a-f0-9]{64}$/.test(hash)) fail('invalid input group member');
    if (createHash('sha256').update(readFileSync(regular(rel))).digest('hex') !== hash) fail('changed input: ' + rel);
  }
}
const harness = ['README.md','verify.mjs','web/playwright.config.ts',
  'web/scripts/webkit-test-data.ts','web/scripts/serve-public-bundle.mjs',
  'web/package.json','web/pnpm-lock.yaml','web/pnpm-workspace.yaml'].map(p => 'qualification/' + p);
if (!equal(Object.keys(payload.harness).sort(), harness.sort())) fail('unexpected harness set');
const expected = ['qualification/payload.json', ...Object.keys(payload.test_files), ...fixtures, ...harness].sort();
if (!equal(inventory('qualification'), expected)) fail('unexpected qualification input');
if (!equal(Object.keys(payload.fixtures).sort(), fixtures.sort())) fail('unexpected fixture set');
if (!equal(inventory('internal/webdist/bundle'), Object.keys(payload.assets).sort())) fail('unexpected or missing asset');
if (!equal(inventory('qualification/web/tests'), Object.keys(payload.test_files).sort())) fail('unexpected or missing test');
if (!Object.keys(payload.test_files).some(p => p.endsWith('.spec.ts')) || Object.keys(payload.test_files).some(p => !p.endsWith('.ts'))) fail('unexpected test type');
for (const rel of ['qualification/curriculum','qualification/passport_system','qualification/web/dist']) {
  if (!equal(inventory(rel), fixtures.filter(p => p.startsWith(rel + '/')).sort())) fail('unexpected fixture input');
}
const source = JSON.parse(readFileSync(regular('SOURCE.json'), 'utf8'));
if (source.source_exact !== true || source.source_commit !== payload.source_ref || source.curriculum_version !== payload.curriculum_version) fail('launcher source differs');
if (!readFileSync(regular('internal/webdist/bundle/workbook/index.html'), 'utf8').includes(payload.source_ref)) fail('bundle source differs');
const routes = readFileSync(regular('internal/webdist/bundle/data/routes.json'));
if (JSON.parse(routes).curriculum_version !== payload.curriculum_version || !routes.equals(readFileSync(regular('qualification/web/dist/data/routes.json')))) fail('route fixture differs');
if (!readFileSync(regular('internal/agentverify/contract.json')).equals(readFileSync(regular('qualification/passport_system/agent_contract.json')))) fail('agent fixture differs');
const pkg = JSON.parse(readFileSync(regular('qualification/web/package.json'), 'utf8'));
if (!/^pnpm@\d+\.\d+\.\d+$/.test(pkg.packageManager)) fail('unpinned browser tooling');
console.log(JSON.stringify({source_ref:payload.source_ref, curriculum_version:payload.curriculum_version,
  assets:Object.keys(payload.assets).length, test_files:Object.keys(payload.test_files).length, private_data:false}));
