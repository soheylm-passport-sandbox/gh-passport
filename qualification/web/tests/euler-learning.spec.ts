import { expect, test } from '@playwright/test';

test('Euler creation, submission and results remain different stages', async ({ page }) => {
  await page.goto('/missions/euler-cpu-first-job/');
  await expect(page.getByRole('heading', { name: 'Follow the steps', exact: true })).toBeVisible();
  const creation = page.locator('#step-create-script');
  await expect(creation.locator('code')).toContainText("<<'EOF'");
  await expect(creation.locator('.recipe-observation').first()).toContainText('No job was submitted');
  await expect(page.locator('#step-queue .recipe-instruction')).toContainText('accounting and logs');
  await expect(page.getByRole('link', { name: 'Only if needed: stop this recorded job' })).toHaveAttribute('href', /labs\/euler-first-job\/\?from=euler-cpu-first-job&at=step-queue#stop-the-recorded-job-if-needed/);
  await expect(page.locator('#step-submit-receipt')).toContainText('automatic check does not connect to Euler');
  await expect(page.locator('[data-question]')).toHaveCount(2);
  await expect(page.locator('[data-learning-checks] > p')).toHaveText('One practice question. It does not affect your progress.');
});

test('optional Euler reasoning leaves official answers and network state alone', async ({ page }) => {
  const writes: string[] = [];
  page.on('request', req => { if (req.method() !== 'GET') writes.push(req.url()); });
  await page.goto('/missions/euler-cpu-accounting/');
  const question = page.locator('[data-learning-question="per_cpu_reservation"]');
  await question.getByLabel('Requested 4 GiB total and used 6 GiB; increase resources immediately.').check();
  await expect(question.locator('[data-learning-feedback]')).toContainText('per CPU');
  await question.getByLabel('Requested 12 GiB total; compare observed 6 GiB with that reservation and representative variation.').check();
  await expect(question.locator('[data-learning-feedback]')).toContainText('3 x 4 = 12');
  await expect(page.locator('[data-question] input:checked')).toHaveCount(0);
  expect(writes).toEqual([]);
});

test('Euler explanations and cancellation context are reachable without JavaScript on mobile', async ({ browser }) => {
  const context = await browser.newContext({ javaScriptEnabled: false, viewport: { width: 390, height: 844 } });
  const page = await context.newPage();
  await page.goto('/missions/euler-cpu-accounting/');
  await page.getByText('Reason about the next test', { exact: true }).press('Enter');
  await expect(page.getByText('Four times 16 GiB reserves 64 GiB.', { exact: false })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.goto('/labs/euler-first-job/#stop-the-recorded-job-if-needed');
  await expect(page.getByRole('heading', { name: 'Stop The Recorded Job If Needed' })).toBeVisible();
  await expect(page.locator('main')).toContainText('Do not cancel a completed successful job');
  await context.close();
});
