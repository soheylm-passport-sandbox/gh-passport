import {expect,test} from '@playwright/test';

test('topic overview is optional and an existing local Passport offers resume',async({page})=>{
 await page.route('**/__passport/v1/context',route=>route.fulfill({json:{mode:'local',repository:{branch:'onboarding/student',passport:{github_user:'student',assessment_branch:'onboarding/student'}},local_state:{last_opened_mission:'git-manual-pr'}}}));
 await page.goto('/');
 const topics=page.locator('details.coverage-section');
 await expect(topics).not.toHaveAttribute('open','');
 await expect(topics.locator('.track-gallery')).toBeHidden();
 await expect(page.locator('main').getByRole('link',{name:'Resume my passport',exact:true})).toHaveAttribute('href','/passport/');
 await topics.locator('summary').press('Enter');
 await expect(topics.locator('.track-gallery')).toBeVisible();
});

test('responsibility explanations do not choose work and cleared preview returns to core',async({page})=>{
 const writes:string[]=[];page.on('request',r=>{if(r.method()!=='GET')writes.push(r.url());});
 await page.goto('/start/');
 const git=page.getByLabel('Modify or review files through GitHub',{exact:true});
 const group=page.locator('.responsibility-choice').filter({has:git});
 await group.locator('summary').press('Enter');
 await expect(group.locator('details p')).toBeVisible();
 await expect(git).not.toBeChecked();
 await git.check();
 await page.getByRole('button',{name:'Show sample lesson list'}).click();
 await expect(page.locator('.route-result')).toContainText('Complete A Manual Pull Request Loop');
 await page.getByRole('button',{name:'Clear preview choices'}).click();
 await expect(git).not.toBeChecked();
 await page.getByRole('button',{name:'Show sample lesson list'}).click();
 await expect(page.locator('.route-result')).not.toContainText('Complete A Manual Pull Request Loop');
 await expect(page.locator('.route-result')).toContainText('Start And Resume Your Passport');
 expect(writes).toEqual([]);
});

test('mobile without JavaScript can open topics and find the private incident route',async({browser})=>{
 const context=await browser.newContext({javaScriptEnabled:false,viewport:{width:390,height:844}});const page=await context.newPage();
 await page.goto('/');
 await page.locator('details.coverage-section summary').press('Enter');
 await expect(page.locator('.track-gallery')).toBeVisible();
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 await page.goto('/help/');
 await expect(page.getByRole('link',{name:'private incident route',exact:true})).toHaveAttribute('href','/core/incidents-and-help/#ask-for-help-when');
 await page.getByRole('link',{name:'private incident route',exact:true}).click();
 await expect(page.locator('#ask-for-help-when')).toBeVisible();
 await expect(page.locator('main')).toContainText('Lab project, NAS permission, or research data');
 await context.close();
});
