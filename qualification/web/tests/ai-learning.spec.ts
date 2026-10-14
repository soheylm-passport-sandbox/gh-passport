import { fixtureCurriculum } from "./fixture-curriculum";
import { expect, test } from '@playwright/test';

test('AI setup shows one route, keeps common verification visible and preserves old anchors', async ({page}) => {
  const writes: string[]=[];page.on('request',r=>{if(r.method()!=='GET')writes.push(r.url());});
  await page.goto('/missions/ai-tool-configuration/');
  await expect(page.locator('#step-copilot-benefit')).toBeVisible();
  await expect(page.locator('#step-optional-paid')).toBeHidden();
  await page.getByRole('button',{name:'Zed (optional paid alternative)',exact:true}).click();
  await expect(page.locator('#step-optional-paid')).toBeVisible();
  await expect(page.locator('#step-editor')).toBeHidden();
  await expect(page.locator('#step-prepare')).toBeVisible();
  await expect(page.locator('#step-verify-clean')).toBeVisible();
  await expect(page.locator('[data-question]')).toHaveCount(2);
  await expect(page.locator('[data-question] input:checked')).toHaveCount(0);
  await expect(page.getByRole('button',{name:'Submit lesson'})).toBeDisabled();
  expect(writes).toEqual([]);
  await page.goto('/missions/ai-tool-configuration/#step-optional-paid');
  await expect(page.locator('#step-optional-paid')).toBeVisible();
  await expect(page.getByRole('button',{name:'Zed (optional paid alternative)',exact:true})).toHaveAttribute('aria-pressed','true');
});

test('changing the AI setup display preserves an existing official draft without saving a route', async ({page}) => {
  const mission='ai-tool-configuration';
  const state={schema_version:1,passport_id:'opaque',curriculum_version:fixtureCurriculum,route_digest:'digest',last_opened_mission:mission,expanded_help:[],mission_drafts:{[mission]:{gateway_role:['wrong_a']}},attempt_counts:{},pending_submissions:{},launch_count:2};
  const writes:string[]=[];
  await page.route('**/__passport/v1/context',route=>route.fulfill({contentType:'application/json',body:JSON.stringify({mode:'local',repository:{owner:'student',name:'passport-exercises',branch:'onboarding/student',head_sha:'a'.repeat(40),passport:{schema_version:2,curriculum_version:fixtureCurriculum,github_user:'student',source_repository:'soheylm-passport-sandbox/passport-exercises',fork_repository:'student/passport-exercises',assessment_branch:'onboarding/student',platform:'linux',responsibilities:['ai_agents'],missions:[mission],setup_complete:true}},local_state:state,official:{status:{current_mission:mission,head_sha:'a'.repeat(40),stage:'in_progress'}}})}));
  await page.route('**/__passport/v1/state',async route=>{writes.push(route.request().postData()||'');await route.fulfill({contentType:'application/json',body:'{}'});});
  await page.goto(`/missions/${mission}/`);
  await expect(page.locator('[data-question="gateway_role"] input[value="wrong_a"]')).toBeChecked();
  await page.getByRole('button',{name:'Zed (optional paid alternative)',exact:true}).click();
  await page.getByRole('button',{name:'VS Code / Copilot',exact:true}).click();
  await expect(page.locator('[data-question="gateway_role"] input[value="wrong_a"]')).toBeChecked();
  expect(writes).toEqual([]);
  expect(state.mission_drafts[mission]).toEqual({gateway_role:['wrong_a']});
});

test('AI setup remains readable on mobile without JavaScript, including both deliberate choices', async ({browser}) => {
  const context=await browser.newContext({javaScriptEnabled:false,viewport:{width:390,height:844}});const page=await context.newPage();
  await page.goto('/missions/ai-tool-configuration/');
  await expect(page.locator('#step-copilot-benefit')).toBeVisible();
  await expect(page.locator('#step-optional-paid')).toBeVisible();
  await expect(page.locator('main')).toContainText('manual alternative is not an AI completion');
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  await context.close();
});
