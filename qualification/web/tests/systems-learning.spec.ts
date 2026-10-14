import { expect, test } from '@playwright/test';

test('systems lesson follows fictional files without requesting real access', async ({ page }) => {
  await page.goto('/missions/core-systems-data/');
  await expect(page.getByRole('heading', { name: 'One project, several places' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Work through the decisions' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Prepare practice folder' })).toHaveCount(0);
  await expect(page.locator('[data-recipe-step]')).toHaveCount(7);
  await expect(page.locator('[data-question]')).toHaveCount(6);
  await expect(page.locator('[data-learning-question]')).toHaveCount(2);
  await expect(page.locator('.recipe-heading')).toContainText('Do not move real files');
  await expect(page.locator('#step-answer .recipe-recovery')).toContainText('They do not verify a real folder');
  await expect(page.locator('.learning-challenge details')).not.toHaveAttribute('open');
  await expect(page.getByRole('button', { name: 'Submit lesson' })).toBeDisabled();
});

test('systems practice gives a hint and leaves the official answer untouched', async ({ page }) => {
  const writes: string[] = [];
  page.on('request', request => { if (request.method() !== 'GET') writes.push(request.url()); });
  await page.goto('/missions/core-systems-data/');
  const official = page.locator('[data-question="source_code"]').getByLabel('In an approved GitHub repository.');
  await official.check();
  const practice = page.locator('[data-learning-question="verified_temporary_copy"]');
  await practice.getByLabel('Yes; the checksum makes scratch durable.').check();
  await expect(practice.locator('[data-learning-feedback]')).toContainText('What happens if scratch is purged');
  await practice.getByLabel('No; verify a copy in the owner-approved durable location before cleanup.').check();
  await expect(practice.locator('[data-learning-feedback]')).toContainText('It does not change storage lifetime');
  await expect(practice.locator('[data-learning-explanation]')).toBeHidden();
  await expect(official).toBeChecked();
  expect(writes).toEqual([]);
});

test('fictional project and explanations remain usable on mobile without JavaScript', async ({ browser }) => {
  const context = await browser.newContext({ javaScriptEnabled: false, viewport: { width: 390, height: 844 } });
  const page = await context.newPage();
  await page.goto('/missions/core-systems-data/');
  await expect(page.locator('.learning-challenge table')).toContainText('measurements.csv');
  const model = page.locator('.learning-challenge details');
  await model.locator('summary').focus();
  await model.locator('summary').press('Enter');
  await expect(model).toHaveAttribute('open', '');
  await expect(model).toContainText('result returns to approved durable storage');
  const explanation = page.locator('[data-learning-question="verified_temporary_copy"] details');
  await explanation.locator('summary').focus();
  await explanation.locator('summary').press('Enter');
  await expect(explanation).toContainText('does not change storage lifetime');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await context.close();
});
