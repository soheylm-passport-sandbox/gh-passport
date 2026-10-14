import {expect,test} from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import {fixtureCurriculum} from './fixture-curriculum';
const mission='ai-agent-baseline';
const correct:Record<string,string[]>=['7.0.0','8.0.0','9.0.0'].includes(fixtureCurriculum)?{file_instruction:['keep_scope'],approve_action:['reject_command'],verify_scope:['inspect_extra']}:{agent_scope:['correct'],agent_result:['correct']};
const draftQuestion=['7.0.0','8.0.0','9.0.0'].includes(fixtureCurriculum)?'file_instruction':'agent_scope';
const draftChoice=['7.0.0','8.0.0','9.0.0'].includes(fixtureCurriculum)?'follow_file':'wrong_a';
for(const width of [1280,390]) {
 test(`AI review allows correction and bounded full-size image return at ${width}px`,async({page})=>{
  test.skip(!['7.0.0','8.0.0','9.0.0'].includes(fixtureCurriculum),'Older courses retain their original two questions.');
  await page.setViewportSize({width,height:900});const writes:string[]=[];page.on('request',r=>{if(r.method()!=='GET')writes.push(r.url());});
  await page.goto(`/missions/${mission}/`);await expect(page.locator('[data-question]')).toHaveCount(3);await expect(page.locator('[data-learning-question]')).toHaveCount(0);
  await expect(page.locator('[data-prepare-practice]')).toBeDisabled();await expect(page.locator('[data-question] input:checked')).toHaveCount(0);
  for(const [id,value] of [['file_instruction','follow_file'],['approve_action','approve_once'],['verify_scope','trust_check']])await page.locator(`[data-question="${id}"] input[value="${value}"]`).check();
  await page.locator('[data-check-work]').click();await expect(page.locator('[data-activity-result]')).toContainText('Review the sample feedback');
  await expect(page.locator('[data-feedback-for="verify_scope"]')).toContainText('not the whole repository');
  for(const [id,values] of Object.entries(correct)){await page.locator(`[data-question="${id}"] input[value="${values[0]}"]`).focus();await page.keyboard.press('Space');}
  await page.locator('[data-check-work]').click();await expect(page.locator('[data-activity-result]')).toContainText('Sample answers correct');await expect(page.locator('[data-submit-mission]')).toBeDisabled();
  const imagesHelp=page.locator('details').filter({has:page.getByText('Optional: see the files and a comparison in VS Code',{exact:true})});await expect(imagesHelp).not.toHaveAttribute('open');await imagesHelp.locator('summary').press('Enter');
  for(const image of await imagesHelp.locator('img').all()) {
   // Bring the expanded images into view and wait for their full load.
   await image.scrollIntoViewIfNeeded();
   await expect.poll(()=>image.evaluate((img:HTMLImageElement)=>img.complete&&img.naturalWidth===2400)).toBe(true);
   await image.evaluate(async(img:HTMLImageElement)=>{await img.decode();});
   expect(await image.evaluate((img:HTMLImageElement)=>img.naturalWidth)).toBe(2400);
  }
  const imageLink=page.getByRole('link',{name:'Open the comparison at full size',exact:true});await imageLink.click();expect(new URL(page.url()).pathname).toBe('/lesson-images/agent-baseline/diff.jpg');await page.goBack();await expect(page.locator('[data-question]')).toHaveCount(3);
  await expect(page.locator('#step-inspect-state')).toContainText('Earlier work remains unchanged');await expect(page.locator('#step-inspect-diff code').first()).toContainText('git diff HEAD');
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);const axe=await new AxeBuilder({page}).analyze();expect(axe.violations.filter(v=>['serious','critical'].includes(v.impact||''))).toEqual([]);expect(writes).toEqual([]);
 });
 test(`AI local draft and pending protection keep earlier work at ${width}px`,async({page})=>{
  await page.setViewportSize({width,height:900});let ready=false;
  const state:any={schema_version:1,passport_id:'synthetic',curriculum_version:fixtureCurriculum,route_digest:'digest',last_opened_mission:mission,mission_drafts:{[mission]:{[draftQuestion]:[draftChoice]},'git-tools-auth':{git_identity:['correct']}},attempt_counts:{[mission]:2},pending_submissions:{},launch_count:2};
  const context=()=>({mode:'local',computer:{platform:'linux',setup_missions:[]},repository:{owner:'student',name:'passport-exercises',branch:'onboarding/student',head_sha:'a'.repeat(40),passport:{schema_version:2,curriculum_version:fixtureCurriculum,github_user:'student',source_repository:'soheylm-passport-sandbox/passport-exercises',fork_repository:'student/passport-exercises',assessment_branch:'onboarding/student',platform:'linux',responsibilities:['ai_agents'],missions:[mission],setup_complete:true}},local_state:state,official:{status:{current_mission:mission,head_sha:'a'.repeat(40),stage:'in_progress'}}});
  await page.route('**/__passport/v1/context',r=>r.fulfill({json:context()}));await page.route('**/__passport/v1/state',async r=>{const next=r.request().postDataJSON();if(JSON.stringify(next.pending_submissions)!==JSON.stringify(state.pending_submissions)){await r.fulfill({status:403,json:{error:'official_sync_fields_are_read_only'}});return;}Object.assign(state,next);await r.fulfill({json:{status:'saved_locally_not_completion'}});});
  await page.route('**/__passport/v2/verify',r=>r.fulfill({json:{status:ready?'ready_to_submit':'needs_work',score_percent:100,critical_passed:true,next_variant:1,feedback:[],receipt:{verifier:'ai_project',passed:ready,checks:{bounded_file:true,bounded_diff:ready,canary_unchanged:true,durable_p:true,temporary_d:true,avoid_c:true,heavy_compute:true},feedback:ready?'':'Inspect the tracked changes inside agent_task.'}}}));
  await page.goto(`/missions/${mission}/`);await expect(page.locator(`[data-question="${draftQuestion}"] input[value="${draftChoice}"]`)).toBeChecked();
  for(const [id,values] of Object.entries(correct))await page.locator(`[data-question="${id}"] input[value="${values[0]}"]`).check();for(const input of await page.locator('.mission-attestation input').all())await input.check();
  await page.locator('[data-check-work]').click();await expect(page.locator('[data-submit-mission]')).toBeDisabled();ready=true;await page.locator('[data-check-work]').click();await expect(page.locator('[data-submit-mission]')).toBeEnabled();
  expect(state.mission_drafts['git-tools-auth']).toEqual({git_identity:['correct']});
  state.pending_submissions={[mission]:{head_sha:'b'.repeat(40),submitted_at:'2026-10-13T09:00:00Z'}};await page.reload();await expect(page.locator('[data-check-work]')).toBeDisabled();await expect(page.locator('[data-submit-mission]')).toBeDisabled();expect(state.pending_submissions[mission].head_sha).toBe('b'.repeat(40));
 });
}
test('AI review images and instructions remain accessible without JavaScript',async({browser})=>{
 test.skip(!['7.0.0','8.0.0','9.0.0'].includes(fixtureCurriculum),'The stronger lesson belongs to 7.0.0.');const context=await browser.newContext({javaScriptEnabled:false,viewport:{width:390,height:900}});const page=await context.newPage();await page.goto(`/missions/${mission}/`);await expect(page.locator('[data-question]')).toHaveCount(3);await expect(page.locator('[data-submit-mission]')).toBeDisabled();await expect(page.locator('#step-prepare')).toContainText('note existing work');expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await context.close();
});
