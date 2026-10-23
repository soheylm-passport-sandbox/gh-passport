import { expect, test, type Page } from '@playwright/test';

const openStagingGuideFromSearch = async (page: Page) => {
  await page.getByRole('button', { name: 'Search handbook' }).click();
  await page.locator('[data-search-kind]').selectOption('reference');
  await page.locator('#global-search-input').fill('staging');
  const guide = page.locator('.search-results a[href^="/reference/guides/git_workflow/"]');
  await expect(guide).toBeVisible();
  await guide.focus();
  await guide.press('Enter');
};

test('a guide found through search returns to the current lesson', async ({ page }) => {
  const writes: string[] = [];
  page.on('request', r => { if (r.method() !== 'GET') writes.push(r.url()); });
  await page.goto('/missions/git-manual-pr/#step-stage');
  await openStagingGuideFromSearch(page);
  const back = page.locator('[data-reference-return] a');
  await expect(back).toHaveAttribute('href', '/missions/git-manual-pr/#lesson-practice');
  await page.reload();
  await back.click();
  await expect(page).toHaveURL(/\/missions\/git-manual-pr\/#lesson-practice$/);
  expect(writes).toEqual([]);
});

test('search preserves a valid reference origin and rejects an injected one', async ({ page }) => {
  await page.goto('/reference/euler/slurm/?from=git-tools-auth&at=lesson-practice&setup=computer');
  await openStagingGuideFromSearch(page);
  await expect(page.locator('[data-reference-return] a')).toHaveAttribute('href', '/missions/git-tools-auth/?setup=computer#lesson-practice');
  await page.reload();
  await expect(page.locator('[data-reference-return] a')).toBeVisible();
  await page.goto('/reference/euler/slurm/?from=https%3A%2F%2Fevil.example&at=lesson-practice');
  await openStagingGuideFromSearch(page);
  await expect(page.locator('[data-reference-return]')).toBeHidden();
  await expect(page).toHaveURL(/\/reference\/guides\/git_workflow\/$/);
});
