import { mkdtempSync, writeFileSync, readFileSync, lstatSync, readdirSync } from "node:fs";
import { rm } from "node:fs/promises";
import { execFileSync } from "node:child_process";
import { tmpdir } from "node:os";
import { join, dirname, basename, resolve } from "node:path";

const rootVariable = "PASSPORT_FIREFOX_TEST_ROOT";
const prefix = "ideal-passport-firefox-tests-";
const marker = ".it-passport-owned-test-data";
const ownership = "Temporary Firefox app-data for IT Passport tests\n";

function verifiedRoot(value: string): string {
  const root = resolve(value);
  if (dirname(root) !== resolve(tmpdir()) || !basename(root).startsWith(prefix)
      || lstatSync(root).isSymbolicLink() || readFileSync(join(root, marker), "utf8") !== ownership) {
    throw new Error("Refusing an unowned Firefox test-data folder");
  }
  return root;
}

// https://github.com/microsoft/playwright/issues/42768
// Isolate app-data as well as profiles; never request access to personal Firefox
// data or change HOME, browser sandbox settings, or macOS privacy permissions.
export function firefoxLaunchOptions() {
  if (process.platform !== "darwin") return undefined;
  let root = process.env[rootVariable];
  if (root === undefined) {
    root = mkdtempSync(join(tmpdir(), prefix));
    writeFileSync(join(root, marker), ownership, { mode: 0o600 });
    process.env[rootVariable] = root;
  }
  root = verifiedRoot(root);
  const appData = mkdtempSync(join(root, "worker-"));
  const env: Record<string, string> = {};
  for (const [key, value] of Object.entries(process.env)) {
    if (value !== undefined) env[key] = value;
  }
  env.CFFIXED_USER_HOME = appData;
  return { env };
}

// Run after browser fixtures/workers stop. An exit-event hook can interrupt
// Playwright's IPC shutdown; asynchronous global teardown keeps shutdown normal.
export default async function cleanupFirefoxTestData() {
  if (process.platform !== "darwin") return;
  const root = process.env[rootVariable];
  if (root === undefined) return;
  const owned = verifiedRoot(root);
  // CoreFoundation adds deny-delete ACLs to standard folders, including nested
  // Library folders, in this imitation home. Only traverse our owned temp tree;
  // never follow symlinks or alter personal folders/OS privacy settings.
  const clearTemporaryAcl = (path: string) => {
    const info = lstatSync(path);
    if (info.isSymbolicLink()) return;
    if (info.uid !== process.getuid?.()) throw new Error("Refusing unowned temporary data");
    execFileSync("/bin/chmod", ["-N", path]);
    if (info.isDirectory()) {
      for (const name of readdirSync(path)) clearTemporaryAcl(join(path, name));
    }
  };
  clearTemporaryAcl(owned);
  await rm(owned, { recursive: true, force: true, maxRetries: 10, retryDelay: 100 });
  delete process.env[rootVariable];
}
