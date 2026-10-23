import {mkdtempSync, mkdirSync, writeFileSync, readFileSync, lstatSync, readdirSync} from 'node:fs';
import {rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join, resolve, dirname, basename} from 'node:path';
import {execFileSync} from 'node:child_process';

const key = 'PASSPORT_PUBLIC_WEBKIT_TEST_ROOT';
const prefix = 'passport-webkit-qualification-';
const marker = '.owned-webkit-test-data';
const ownership = 'temporary synthetic browser data\n';
function owned(root: string) {
  const info = lstatSync(root);
  if (dirname(resolve(root)) !== resolve(tmpdir()) || !basename(root).startsWith(prefix)
      || info.isSymbolicLink() || !info.isDirectory() || info.uid !== process.getuid?.()
      || readFileSync(join(root, marker), 'utf8') !== ownership) {
    throw Error('Unowned WebKit data');
  }
  return root;
}
export function webkitLaunchOptions() {
  if (process.platform !== 'darwin') return undefined;
  let root = process.env[key];
  if (!root) {
    root = mkdtempSync(join(tmpdir(), prefix));
    writeFileSync(join(root, marker), ownership, {mode: 0o600});
    process.env[key] = root;
  }
  owned(root);
  const prefs = join(root, 'Library/Preferences');
  mkdirSync(prefs, {recursive: true});
  writeFileSync(join(prefs, 'org.webkit.Playwright.plist'),
    '<?xml version="1.0" encoding="UTF-8"?>' +
    '<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">' +
    '<plist version="1.0"><dict><key>NSAutomaticWindowAnimationsEnabled</key><false/></dict></plist>');
  return {env: {...process.env, CFFIXED_USER_HOME: root}};
}
export default async function cleanup() {
  if (process.platform !== 'darwin') return;
  const root = process.env[key];
  if (!root) return;
  owned(root);
  // CoreFoundation adds deny-delete ACLs. Never follow links or touch user data.
  const clear = (path: string) => {
    const info = lstatSync(path);
    if (info.isSymbolicLink()) return;
    if (info.uid !== process.getuid?.()) throw Error('Unowned temporary data');
    execFileSync('/bin/chmod', ['-N', path]);
    if (info.isDirectory()) for (const name of readdirSync(path)) clear(join(path, name));
  };
  clear(root);
  await rm(root, {recursive: true, force: true, maxRetries: 10, retryDelay: 100});
  delete process.env[key];
}
