import { fixtureCurriculum } from "./fixture-curriculum";
import { expect, test } from '@playwright/test';

test('array and GPU repairs remain local file models across course versions', async ({ page }) => {
  await page.goto('/missions/euler-cpu-arrays/');
  const cap = page.locator('#step-cap');
  await expect(cap.locator('code')).toBeHidden();
  await expect(cap.locator('.command-disclosure .command-disclosure')).toHaveCount(0);
  await cap.locator('.command-disclosure summary').click();
  await expect(cap.locator('code')).toContainText('--array=0-9%1');
  await expect(page.locator('[data-question]')).toHaveCount(3);
  await expect(page.locator('[data-learning-question]')).toHaveCount(['5.0.0','6.0.0','7.0.0','8.0.0','9.0.0'].includes(fixtureCurriculum) ? 0 : 1);
  if (!['5.0.0','6.0.0','7.0.0','8.0.0','9.0.0'].includes(fixtureCurriculum)) await expect(page.locator('[data-learning-checks]')).toContainText('It does not affect your progress.');
  await page.goto('/missions/euler-gpu-review/');
  if(['8.0.0','9.0.0'].includes(fixtureCurriculum)){
    await expect(page.locator('#step-prepare')).toContainText('starter limits');
    await expect(page.locator('[data-question]')).toHaveCount(3);
    await expect(page.locator('#learning-challenge')).toContainText('Runs A / B / C');
  }else{
    await expect(page.locator('#step-resources')).toContainText('Keep this a local text edit');
    await expect(page.locator('[data-question]')).toHaveCount(2);
    await expect(page.locator('#learning-challenge')).toContainText('ceilings, not targets');
  }
});

test('array decisions allow feedback and correction without public submission', async ({ page }) => {
  const writes: string[] = [];
  page.on('request', r => { if (r.method() !== 'GET') writes.push(r.url()); });
  await page.goto('/missions/euler-cpu-arrays/');
  if (['5.0.0','6.0.0','7.0.0','8.0.0','9.0.0'].includes(fixtureCurriculum)) {
    await expect(page.locator('[data-learning-question]')).toHaveCount(0);
    await expect(page.locator('[data-question]')).toHaveCount(3);
    await expect(page.locator('[data-question] input:checked')).toHaveCount(0);
    for (const [id, option] of [['combined_peak', 'array_only'], ['budget_cap', 'cap_four'], ['node_memory', 'unchanged']]) {
      await page.locator(`[data-question="${id}"] input[value="${option}"]`).check();
    }
    await page.getByRole('button', { name: 'Check sample answers', exact: true }).click();
    await expect(page.locator('[data-feedback-for="budget_cap"]')).toContainText('Check both limits');
    for (const [id, option] of [['combined_peak', 'combined'], ['budget_cap', 'cap_two'], ['node_memory', 'node_total']]) {
      await page.locator(`[data-question="${id}"] input[value="${option}"]`).focus();
      await page.keyboard.press('Space');
    }
    await page.getByRole('button', { name: 'Check sample answers', exact: true }).click();
    await expect(page.getByText('Sample answers correct', { exact: true })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Submit lesson', exact: true })).toBeDisabled();
    expect(writes).toEqual([]);
    return;
  }
  const q = page.locator('[data-learning-question="other_active_jobs"]');
  await q.getByLabel('Two CPUs and 6 GiB, because the cap covers every job.').check();
  await expect(q.locator('[data-learning-feedback]')).toContainText('separate job');
  await q.getByLabel('Four CPUs and 12 GiB: two array tasks plus the existing job.').check();
  await expect(q.locator('[data-learning-feedback]')).toContainText('4 CPUs and 12 GiB');
  await expect(page.locator('[data-question] input:checked')).toHaveCount(0);
  expect(writes).toEqual([]);
});

test('storage lifecycle and GPU resource distinctions remain accessible on mobile without JavaScript', async ({ browser }) => {
  const context = await browser.newContext({ javaScriptEnabled: false, viewport: { width: 390, height: 844 } });
  const page = await context.newPage();
  await page.goto('/missions/euler-storage-reproducibility/');
  await expect(page.locator('main')).toContainText('not a real recoverable run record');
  await page.getByText('Why the pending job needs unchanged referenced files', { exact: true }).press('Enter');
  await expect(page.getByText('The submitted batch script is captured', { exact: false })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.goto('/missions/euler-gpu-review/');
  await expect(page.locator('#learning-challenge')).toContainText(['8.0.0','9.0.0'].includes(fixtureCurriculum)?'GPU VRAM peak':'distinct from GPU memory');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await context.close();
});
