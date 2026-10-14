import { expect, test } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { fixtureCurriculum } from './fixture-curriculum';

const current = ['5.0.0','6.0.0','7.0.0','8.0.0','9.0.0'].includes(fixtureCurriculum);
const ids = current ? ['combined_peak', 'budget_cap', 'node_memory'] : ['array_cap', 'array_logs', 'array_aggregate'];
const wrong = current ? ['array_only', 'cap_four', 'unchanged'] : ['wrong_a', 'wrong_a', 'wrong_a'];
const right = current ? ['combined', 'cap_two', 'node_total'] : ['correct', 'correct', 'correct'];

for (const width of [1280, 390]) {
  test(`Euler questions and closed help remain readable without JavaScript at ${width}px`, async ({ browser }) => {
    const context = await browser.newContext({ javaScriptEnabled: false, viewport: { width, height: 844 } });
    const page = await context.newPage();
    await page.goto('/missions/euler-cpu-arrays/');
    await expect(page.locator('[data-question]')).toHaveCount(3);
    for (const id of ids) await expect(page.locator(`[data-question="${id}"]`)).toBeVisible();
    const help = page.locator('#learning-challenge details').first();
    await help.locator('summary').focus(); await page.keyboard.press('Enter');
    await expect(help).toHaveAttribute('open', '');
    if (current) await expect(help).toContainText('memory per node');
    await expect(page.getByRole('button', { name: 'Submit lesson', exact: true })).toBeDisabled();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await context.close();
    // Axe executes JavaScript; keep that scan separate from the no-JS journey.
    const accessible = await browser.newContext({ viewport: { width, height: 844 } });
    const rendered = await accessible.newPage();
    await rendered.goto('/missions/euler-cpu-arrays/');
    const axe = await new AxeBuilder({ page: rendered }).analyze();
    expect(axe.violations.filter(v => ['serious', 'critical'].includes(v.impact || ''))).toEqual([]);
    await accessible.close();
  });
}

// UI state tests use the contract actually served; arithmetic concordance is
// covered separately by actual Go HTTP and controller tests, not this mock.
for (const stage of ['draft', 'pending', 'complete']) {
  test(`Euler ${stage} state keeps the assigned answers and submission gate`, async ({ page }) => {
    const mission = 'euler-cpu-arrays';
    const answers = Object.fromEntries(ids.map((id, i) => [id, [stage === 'complete' ? right[i] : wrong[i]]]));
    let state = { schema_version: 1, passport_id: 'opaque', curriculum_version: fixtureCurriculum, route_digest: 'digest', last_opened_mission: mission, expanded_help: [], mission_drafts: { [mission]: answers }, attempt_counts: { [mission]: 2 }, pending_submissions: stage === 'pending' ? { [mission]: { head_sha: 'b'.repeat(40), submitted_at: '2026-10-12T09:00:00Z' } } : {}, launch_count: 2 };
    const saved: unknown[] = [];
    await page.route('**/__passport/v1/state', async route => {
      if (route.request().method() === 'PUT') { state = route.request().postDataJSON(); saved.push(state); }
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify(state) });
    });
    await page.route('**/__passport/v1/context', route => route.fulfill({ contentType: 'application/json', body: JSON.stringify({
      mode: 'local', repository: { owner: 'student', name: 'passport-exercises', branch: 'onboarding/student', head_sha: 'a'.repeat(40), passport: { schema_version: 2, curriculum_version: fixtureCurriculum, github_user: 'student', source_repository: 'soheylm-passport-sandbox/passport-exercises', fork_repository: 'student/passport-exercises', assessment_branch: 'onboarding/student', platform: 'linux', responsibilities: ['euler_cpu'], missions: [mission], setup_complete: true } }, local_state: state,
      official: { status: { schema_version: 2, curriculum_version: fixtureCurriculum, head_sha: 'a'.repeat(40), current_mission: stage === 'complete' ? null : mission, stage: stage === 'complete' ? 'complete' : 'working', missions: [{ id: mission, status: stage === 'complete' ? 'passed' : 'needs_work' }] } },
    }) }));
    await page.goto('/missions/euler-cpu-arrays/');
    if (stage === 'complete') {
      await expect(page.getByText('Course already complete', { exact: true })).toBeVisible();
      expect(state.mission_drafts[mission]).toEqual(answers);
    } else {
      for (let i = 0; i < ids.length; i++) {
        await expect(page.locator(`[data-question="${ids[i]}"] input[value="${wrong[i]}"]`)).toBeChecked();
      }
    }
    await expect(page.getByRole('button', { name: 'Submit lesson', exact: true })).toBeDisabled();
    if (stage === 'draft') {
      await expect(page.getByRole('button', { name: 'Check my work', exact: true })).toBeEnabled();
      await page.locator(`[data-question="${ids[0]}"] input[value="${right[0]}"]`).check();
      await expect.poll(() => saved.length).toBeGreaterThan(0);
      expect(state.mission_drafts[mission][ids[0]]).toEqual([right[0]]);
      expect(state.curriculum_version).toBe(fixtureCurriculum);
    } else {
      await expect(page.getByRole('button', { name: 'Check my work', exact: true })).toBeDisabled();
      expect(saved).toEqual([]);
      if (stage === 'pending') expect(state.pending_submissions[mission]?.head_sha).toBe('b'.repeat(40));
    }
  });
}
