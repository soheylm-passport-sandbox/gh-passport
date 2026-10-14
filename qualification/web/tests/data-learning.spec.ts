import { fixtureCurriculum } from "./fixture-curriculum";
import { expect, test } from '@playwright/test';

test('fictional data decisions retain their official questions and reveal each model once', async ({ page }) => {
  const counts: Record<string,number> = {'data-placement':6,'data-collaboration':6,'data-handover':['6.0.0','7.0.0','8.0.0','9.0.0'].includes(fixtureCurriculum)?3:2,'supervisor-onboarding':7,'supervisor-offboarding':7};
  for (const [id,count] of Object.entries(counts)) {
    await page.goto(`/missions/${id}/`);
    await expect(page.locator('[data-question]')).toHaveCount(count);
    await expect(page.locator('main')).toContainText(/fictional/i);
    await expect(page.getByRole('button',{name:'Submit lesson'})).toBeDisabled();
  }
  await page.goto('/missions/data-handover/');
  await expect(page.locator('.machine-callout')).toContainText('On this web page and your computer');
  const model = page.locator(['6.0.0','7.0.0','8.0.0','9.0.0'].includes(fixtureCurriculum)?'#step-packet':'#step-ownership');
  await expect(model.locator('code').first()).toBeHidden();
  await model.locator('.command-disclosure summary').first().click();
  await expect(model.locator('code').first()).toBeVisible();
  await expect(model.locator('.command-disclosure .command-disclosure')).toHaveCount(0);
});

test('offboarding shows selected sequence and duplicate actions without changing choices', async ({page}) => {
  const writes: string[]=[];page.on('request',r=>{if(r.method()!=='GET')writes.push(r.url());});
  await page.goto('/missions/supervisor-offboarding/');
  const order=page.locator('[data-question="offboarding_order"]');
  await order.locator('select').nth(0).selectOption('inventory');
  await expect(order.locator('[data-order-summary]')).toContainText('Inventory systems');
  await expect(order.locator('[data-order-summary]')).toContainText('not selected');
  await order.locator('select').nth(1).selectOption('inventory');
  await expect(order.locator('[data-order-summary]')).toContainText('more than once');
  await order.locator('select').nth(1).selectOption('transfer');
  await expect(order.locator('[data-order-summary]')).toContainText('Transfer ownership');
  await expect(order.locator('select').nth(0)).toHaveValue('inventory');
  await expect(order.locator('select').nth(1)).toHaveValue('transfer');
  expect(writes).toEqual([]);
});

test('data decisions remain readable on mobile without JavaScript', async ({browser})=>{
  const context=await browser.newContext({javaScriptEnabled:false,viewport:{width:390,height:844}});const page=await context.newPage();
  await page.goto('/missions/data-placement/');
  await expect(page.locator('.learning-challenge table')).toContainText('Mesh cache');
  await page.locator('.learning-challenge details summary').press('Enter');
  await expect(page.locator('.learning-challenge details')).toContainText('backup/recovery and retention');
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  await context.close();
});

test('an existing offboarding sequence is restored and a changed order preserves other draft answers', async ({page})=>{
  const mission='supervisor-offboarding';
  const saved=['inventory','transfer','verify','cleanup','rotate','revoke','record'];
  let state={schema_version:1,passport_id:'opaque',curriculum_version:fixtureCurriculum,route_digest:'digest',last_opened_mission:mission,expanded_help:[],mission_drafts:{[mission]:{offboarding_order:saved,personal_dependency:['correct']}},attempt_counts:{},pending_submissions:{},launch_count:2};
  const context=()=>({mode:'local',repository:{owner:'student',name:'passport-exercises',branch:'onboarding/student',head_sha:'a'.repeat(40),passport:{schema_version:2,curriculum_version:fixtureCurriculum,github_user:'student',source_repository:'soheylm-passport-sandbox/passport-exercises',fork_repository:'student/passport-exercises',assessment_branch:'onboarding/student',platform:'linux',responsibilities:['supervisor'],missions:[mission],setup_complete:true}},local_state:state,official:{status:{current_mission:mission,head_sha:'a'.repeat(40),stage:'in_progress'}}});
  await page.route('**/__passport/v1/context',route=>route.fulfill({contentType:'application/json',body:JSON.stringify(context())}));
  await page.route('**/__passport/v1/state',async route=>{state=route.request().postDataJSON();await route.fulfill({contentType:'application/json',body:JSON.stringify({status:'saved_locally_not_completion'})});});
  await page.goto(`/missions/${mission}/`);
  const order=page.locator('[data-question="offboarding_order"]');
  await expect(order.locator('[data-order-summary]')).toContainText('Record completion');
  for(let i=0;i<saved.length;i++)await expect(order.locator('select').nth(i)).toHaveValue(saved[i]);
  await order.locator('select').nth(1).selectOption('inventory');
  await expect(order.locator('[data-order-summary]')).toContainText('more than once');
  await expect.poll(()=>state.mission_drafts[mission].offboarding_order[1]).toBe('inventory');
  expect(state.mission_drafts[mission].personal_dependency).toEqual(['correct']);
  await expect(page.getByRole('button',{name:'Submit lesson'})).toBeDisabled();
});
