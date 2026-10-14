import {expect,test} from '@playwright/test';

for (const platform of ['windows','macos','linux']) {
  test(`reference uses the current ${platform} computer while preserving common guidance`, async ({page})=>{
    const writes:string[]=[];page.on('request',r=>{if(r.method()!=='GET')writes.push(r.url());});
    await page.route('**/__passport/v1/context',route=>route.fulfill({json:{mode:'local',computer:{platform},repository:{branch:'onboarding/student',passport:{github_user:'student',assessment_branch:'onboarding/student',platform:platform==='windows'?'macos':'windows'}}}}));
    await page.goto('/reference/guides/nas_ideal/?from=nas-access-storage&at=step-local-path');
    const ids={windows:'windows-1011',macos:'macos-13-or-later',linux:'linux'};
    for (const [os,id] of Object.entries(ids)) {
      if (os===platform) await expect(page.locator(`#${id}`)).toBeVisible();
      else await expect(page.locator(`#${id}`)).toBeHidden();
    }
    await expect(page.locator('#verify-access-safely')).toBeVisible();
    await expect(page.locator('[data-reference-return] a')).toHaveAttribute('href','/missions/nas-access-storage/#step-local-path');
    expect(writes).toEqual([]);
  });
}

test('a deliberate choice wins a delayed context and old OS anchors remain reachable',async({page})=>{
  let release=()=>{};const pending=new Promise<void>(resolve=>{release=resolve;});
  await page.route('**/__passport/v1/context',async route=>{await pending;await route.fulfill({json:{mode:'local',computer:{platform:'linux'},repository:{branch:'onboarding/student',passport:{github_user:'student',assessment_branch:'onboarding/student',platform:'linux'}}}});});
  await page.goto('/reference/guides/python_setup/');
  await page.getByRole('button',{name:'macOS',exact:true}).click();release();
  await expect(page.locator('[data-runtime-label]')).toHaveText('Your local passport');
  await expect(page.locator('#install-miniforge-on-macos')).toBeVisible();
  await expect(page.locator('#install-miniforge-on-linux')).toBeHidden();
  await page.getByText('Find a section on this page',{exact:true}).click();
  await page.getByRole('navigation',{name:'Sections on this page'}).getByRole('link',{name:'Install Miniforge On Windows',exact:true}).click();
  await expect(page.locator('#install-miniforge-on-windows')).toBeVisible();
  await expect(page.getByRole('button',{name:'Windows',exact:true})).toHaveAttribute('aria-pressed','true');
  await page.reload();
  await expect(page.locator('#install-miniforge-on-windows')).toBeVisible();
});

test('common remote Bash and safety remain visible when Windows local instructions are selected',async({page})=>{
  await page.setViewportSize({width:390,height:844});
  await page.goto('/reference/euler/euler-tunnel/');
  await page.getByRole('button',{name:'Windows',exact:true}).click();
  await expect(page.locator('#windows-laptop---powershell')).toBeVisible();
  await expect(page.locator('#macos-or-linux-laptop---zshbash')).toBeHidden();
  await expect(page.locator('[id="1-generate-personal-configuration"]')).toBeVisible();
  await expect(page.locator('#stop-the-allocation')).toBeVisible();
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
});

test('public and no-JavaScript references retain every OS branch and old section IDs',async({browser,page})=>{
  await page.goto('/reference/guides/nas_ideal/');
  for(const id of ['windows-1011','macos-13-or-later','linux'])await expect(page.locator(`#${id}`)).toBeVisible();
  const context=await browser.newContext({javaScriptEnabled:false,viewport:{width:390,height:844}});const plain=await context.newPage();
  await plain.goto('/reference/guides/nas_ideal/');
  for(const id of ['windows-1011','macos-13-or-later','linux'])await expect(plain.locator(`#${id}`)).toBeVisible();
  await expect(plain.locator('[data-reference-platform-picker]')).toBeHidden();
  expect(await plain.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  await context.close();
});
