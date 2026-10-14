import { expect, test } from '@playwright/test';
import {fixtureCurriculum} from './fixture-curriculum';
import AxeBuilder from '@axe-core/playwright';

for (const width of [1280,390]) {
  test(`project guidance stays separate from lesson assessment at ${width}px`,async({page})=>{
    await page.setViewportSize({width,height:844});
    for (const id of ['ai-tool-configuration','ai-agent-baseline','git-tools-auth']) {
      await page.goto(`/missions/${id}/`);
      const card=page.locator('[data-project-guidance]');await expect(card).toBeVisible();
      await expect(page.locator('[data-activity-form]')).not.toContainText('For your lab projects');
      await expect(page.locator('[data-question]')).toHaveCount(id==='ai-agent-baseline'&&['7.0.0','8.0.0','9.0.0'].includes(fixtureCurriculum)?3:2);
      await expect(page.locator('[data-question] input:checked')).toHaveCount(0);
      await expect(page.getByRole('button',{name:'Submit lesson',exact:true})).toBeDisabled();
      const link=card.getByRole('link').first();await link.click();
      await expect(page.locator('h1')).toHaveText(id==='git-tools-auth'?'Use A GitHub SSH Key For Lab Projects':'Give Your AI Agent The Lab Context');
      if(id!=='git-tools-auth')await expect(page.locator('main')).toContainText('chat, not the terminal');
      expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
      const axe=await new AxeBuilder({page}).analyze();
      expect(axe.violations.filter(v=>['serious','critical'].includes(v.impact||''))).toEqual([]);
    }
  });
}

test('project guidance remains visible without JavaScript',async({browser})=>{
  const context=await browser.newContext({javaScriptEnabled:false,viewport:{width:390,height:844}});
  const page=await context.newPage();await page.goto('/missions/ai-agent-baseline/');
  const card=page.locator('[data-project-guidance]');await expect(card).toBeVisible();
  await card.getByRole('link').click();await expect(page.locator('h1')).toHaveText('Give Your AI Agent The Lab Context');
  await context.close();
});

test('SSH platform display keeps shared verification visible and no-JavaScript alternatives readable',async({page,browser})=>{
  await page.goto('/reference/git/github-ssh/');
  for(const platform of ['Windows','macOS','Linux']) {
    await page.getByRole('button',{name:platform,exact:true}).click();
    await expect(page.locator('#check-the-reply')).toBeVisible();
    await expect(page.locator('#continue-after-cloning')).toBeVisible();
    await expect(page.locator('main')).toContainText('exit code 1');
    const other=platform==='Windows'?'linux':'windows';
    await expect(page.locator(`[data-reference-platforms="${other}"]`).first()).toBeHidden();
  }
  const context=await browser.newContext({javaScriptEnabled:false});const p=await context.newPage();await p.goto('/reference/git/github-ssh/');
  await expect(p.locator('[data-reference-platforms]')).toHaveCount(11);
  for(const branch of await p.locator('[data-reference-platforms]').all())await expect(branch).toBeVisible();
  await context.close();
});
