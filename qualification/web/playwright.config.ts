import {defineConfig,devices} from '@playwright/test';
const port=Number(process.env.PASSPORT_PUBLIC_TEST_PORT || '4321');
export default defineConfig({
 testDir:'./tests',fullyParallel:true,forbidOnly:true,retries:1,workers:4,
 reporter:[['line'],['html',{open:'never'}]],
 use:{baseURL:`http://127.0.0.1:${port}`,trace:'retain-on-failure',screenshot:'only-on-failure',video:'retain-on-failure',colorScheme:'light'},
 projects:[{name:'webkit',timeout:60_000,use:{...devices['Desktop Safari']}}],
 webServer:{command:'node scripts/serve-public-bundle.mjs',url:`http://127.0.0.1:${port}`,reuseExistingServer:false,timeout:30000},
});
