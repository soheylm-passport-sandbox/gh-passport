import { expect, test } from '@playwright/test';
import { mkdtempSync, mkdirSync, rmSync, realpathSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';
import { fixtureCurriculum } from './fixture-curriculum';

const localContext = (mission: string, platform: string, complete = false) => ({
  mode: 'local',
  computer: { platform, setup_missions: [mission] },
  repository: {
    owner: 'student', name: 'passport-exercises', branch: 'onboarding/student',
    head_sha: 'a'.repeat(40),
    passport: {
      schema_version: 2, curriculum_version: fixtureCurriculum,
      github_user: 'student', source_repository: 'soheylm-passport-sandbox/passport-exercises',
      fork_repository: 'student/passport-exercises', assessment_branch: 'onboarding/student',
      platform, responsibilities: ['git', 'data_steward'], missions: [mission], setup_complete: true,
    },
  },
  local_state: {
    schema_version: 1, passport_id: 'synthetic', curriculum_version: fixtureCurriculum,
    route_digest: 'digest', last_opened_mission: mission, expanded_help: [],
    mission_drafts: {}, attempt_counts: {}, pending_submissions: {}, launch_count: 2,
  },
  official: { status: { current_mission: complete ? null : mission, head_sha: 'a'.repeat(40), stage: complete ? 'complete' : 'in_progress' } },
});

for (const platform of ['linux', 'macos', 'windows']) {
  test(`copied practice-folder command enters a literal path on ${platform}`, async ({ page }) => {
    // Only an owned fictional directory is created; no user configuration is read.
    const root = mkdtempSync(join(process.platform === 'win32' ? tmpdir() : '/tmp', 'passport-path-'));
    const path = join(root, "Student's $HOME `name` [demo]");
    mkdirSync(path);
    try {
      await page.route('**/__passport/v1/context', r => r.fulfill({ json: localContext('git-manual-pr', platform) }));
      await page.route('**/__passport/v1/state', r => r.fulfill({ json: { status: 'saved_locally_not_completion' } }));
      await page.route('**/__passport/v2/practice', r => r.fulfill({ json: { status: 'ready', path, branch: 'practice/student' } }));
      await page.addInitScript(() => {
        Object.defineProperty(navigator, 'clipboard', { value: { writeText: async (text: string) => sessionStorage.setItem('copied-path', text) } });
      });
      await page.goto('/missions/git-manual-pr/');
      await page.locator('[data-prepare-practice]').click();
      const command = (await page.locator('[data-practice-commands] code').textContent())!;
      await page.locator('[data-practice-commands] [data-copy-command]').click();
      await expect.poll(() => page.evaluate(() => sessionStorage.getItem('copied-path'))).toBe(command);
      // A UI-only string assertion would miss expansion or wildcard handling.
      const shell = platform === 'windows' ? 'pwsh' : platform === 'macos' ? 'zsh' : 'bash';
      const args = platform === 'windows'
        ? ['-NoLogo', '-NoProfile', '-NonInteractive', '-Command', `$ErrorActionPreference='Stop'; ${command}; [Console]::Write((Get-Location).ProviderPath)`]
        : [platform === 'macos' ? '-f' : '--noprofile', ...(platform === 'linux' ? ['--norc'] : []), '-c', `${command} && pwd -P`];
      const probe = process.platform === 'win32' && platform !== 'windows' ? null : spawnSync(shell, args, { cwd: root, encoding: 'utf8', timeout: 20_000 });
      if (!probe || (probe.error as NodeJS.ErrnoException)?.code === 'ENOENT') {
        test.info().annotations.push({ type: 'shell-unavailable', description: `${shell}: copied command checked in browser; native execution unavailable on this runner.` });
        expect(command).toContain("'");
        expect(command).not.toContain(`"${path}"`);
      } else {
        expect(probe.error).toBeUndefined();
        expect(probe.status, probe.stderr).toBe(0);
        expect(realpathSync(resolve(probe.stdout.trim()))).toBe(realpathSync(path));
      }
      await expect(page.locator('[data-submit-mission]')).toBeDisabled();
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });
}

test('handover help returns to computer setup without assessment writes', async ({ page }) => {
  test.skip(!['6.0.0', '7.0.0', '8.0.0', '9.0.0'].includes(fixtureCurriculum), 'Historical handover has no computer preparation route.');
  const writes: string[] = [];
  page.on('request', r => { if (r.method() !== 'GET') writes.push(r.url()); });
  await page.route('**/__passport/v1/context', r => r.fulfill({ json: localContext('data-handover', 'linux', true) }));
  await page.goto('/missions/data-handover/?setup=computer');
  await expect(page.locator('[data-activity-form]')).toBeHidden();
  await page.locator('header nav').getByRole('link', { name: 'Help', exact: true }).click();
  await expect(page).toHaveURL(/\/help\/\?from=data-handover&at=lesson-practice&setup=computer$/);
  const back = page.locator('[data-reference-return] a');
  await expect(back).toHaveAttribute('href', '/missions/data-handover/?setup=computer#lesson-practice');
  await page.reload();
  await expect(back).toBeVisible();
  await back.click();
  await expect(page.locator('[data-computer-setup]')).toBeVisible();
  await expect(page.locator('[data-activity-form]')).toBeHidden();
  await expect(page.locator('[data-prepare-practice]')).toBeEnabled();
  expect(writes).toEqual([]);
});
