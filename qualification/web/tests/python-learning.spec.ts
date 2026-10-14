import { expect, test } from '@playwright/test';

test('Python task lets the learner choose a regression and keeps the failure boundary', async ({ page }) => {
  await page.goto('/missions/python-reproducible-change/');
  const red = page.locator('#step-red-test');
  await expect(red.locator('.command-disclosure')).toHaveCount(0);
  await expect(red.locator('[data-command-mode="run"]:visible code')).toContainText('python -m unittest');
  await expect(red.locator('.recipe-instruction')).toContainText('Choose more than one CPU');
  await expect(red.locator('.recipe-instruction')).toContainText('calculate the expected total yourself');
  await expect(red.locator('.recipe-instruction')).toContainText('An AssertionError');
  await expect(red.locator('.recipe-observation').last()).toContainText('correct the test before editing');
  await expect(page.locator('[data-question]')).toHaveCount(2);
  await expect(page.locator('[data-learning-question]')).toHaveCount(2);
  await expect(page.locator('#step-review code:visible')).toContainText('git diff -- .');
  const resource = page.getByRole('link', { name: 'Optional: function inputs and return values' });
  await expect(resource).toHaveAttribute('href', 'https://www.w3schools.com/python/python_functions.asp');
});

test('Conda recovery is closed, platform-specific and leaves its trigger visible', async ({ page }) => {
  await page.goto('/missions/python-environment/');
  const recovery = page.locator('#step-recover-channel-terms');
  await expect(recovery.locator('.recipe-instruction')).toContainText('CondaToSNonInteractiveError');
  const body = recovery.locator('.command-step:visible .command-disclosure');
  await expect(body.locator('code')).toBeHidden();
  await expect(body.locator('summary')).toHaveCount(1);
  await body.locator('summary').click();
  await expect(body.locator('code')).toContainText('--override-channels --channel conda-forge');
  await expect(body.getByRole('button', { name: 'Copy', exact: true })).toBeVisible();
  await expect(recovery.locator('.recipe-observation').last()).toContainText('Do not disable plugins');
});

test('Python optional checks explain misconceptions without assessment writes', async ({ page }) => {
  const writes: string[] = [];
  page.on('request', req => { if (req.method() !== 'GET') writes.push(req.url()); });
  await page.goto('/missions/python-reproducible-change/');
  const question = page.locator('[data-learning-question="failure_kind"]');
  await question.getByLabel('Change the multiplication now; any red output is sufficient.').check();
  await expect(question.locator('[data-learning-feedback]')).toContainText('reach the assertion');
  await question.getByLabel('Repair the folder/import or environment first, then reproduce the intended assertion failure.').check();
  await expect(question.locator('[data-learning-feedback]')).toContainText('failed import');
  await expect(question.locator('[data-learning-explanation]')).toBeHidden();
  expect(writes).toEqual([]);
});

test('conditional setup and the regression remain inspectable without JavaScript on mobile', async ({ browser }) => {
  const context = await browser.newContext({ javaScriptEnabled: false, viewport: { width: 390, height: 844 } });
  const page = await context.newPage();
  await page.goto('/missions/python-environment/');
  await expect(page.locator('#step-recover-conda .command-disclosure')).toHaveCount(3);
  const first = page.locator('#step-recover-conda .command-disclosure').first();
  await first.locator('summary').focus();
  await first.locator('summary').press('Enter');
  await expect(first.locator('code')).toContainText('STOP: multiple Conda installations');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await context.close();
});

for (const width of [1280, 390]) {
  test(`Python syntax and transfer practice use the lesson page at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 });
    const writes: string[] = [];
    page.on('request', request => { if (request.method() !== 'GET') writes.push(request.url()); });
    await page.goto('/missions/python-reproducible-change/');
    const syntax = page.locator('#python-reading-code');
    await expect(syntax.locator('pre:visible')).toHaveCount(0);
    await syntax.locator('summary').focus();
    await syntax.locator('summary').press('Enter');
    await expect(syntax).toContainText('test_');
    await expect(syntax).toContainText('expected result');
    await expect(page.locator('#step-fix .command-disclosure')).toHaveCount(0);
    await expect(page.locator('#step-fix .recipe-instruction')).toContainText('temporary variable');
    const extra = page.locator('#python-extra-practice');
    await expect(extra.locator('pre').first()).toBeHidden();
    await extra.locator(':scope > summary').click();
    await expect(extra).toContainText('without changing your practice files');
    const codeBlock = extra.locator('pre').first();
    await expect(codeBlock).toHaveAttribute('tabindex', '0');
    await codeBlock.focus();
    if (width === 390) {
      await codeBlock.press('ArrowRight');
      await expect.poll(() => codeBlock.evaluate(element => element.scrollLeft)).toBeGreaterThan(0);
    }
    await expect(extra.locator('input, form, iframe')).toHaveCount(0);
    await extra.locator('details').last().locator('summary').click();
    await expect(extra).toContainText('self.assertEqual(packed_weight_g(4, 50, 30), 230)');
    await expect(page.locator('[data-question]')).toHaveCount(2);
    await expect(page.locator('[data-learning-question]')).toHaveCount(2);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    expect(writes).toEqual([]);
  });
}

test('Python extra practice stays on the page without JavaScript', async ({ browser }) => {
  const context = await browser.newContext({ javaScriptEnabled: false, viewport: { width: 390, height: 844 } });
  const page = await context.newPage();
  await page.goto('/missions/python-reproducible-change/');
  const extra = page.locator('#python-extra-practice');
  await extra.locator(':scope > summary').focus();
  await extra.locator(':scope > summary').press('Enter');
  const solution = extra.locator('details').last();
  await solution.locator('summary').focus();
  await solution.locator('summary').press('Enter');
  await expect(solution).toContainText('box_weight_g');
  await expect(page.locator('#step-red-test')).toContainText('At least two tests run');
  await expect(page.locator('[data-question]')).toHaveCount(2);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await context.close();
});
