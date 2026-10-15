import {mkdtempSync,mkdirSync,writeFileSync,readFileSync,lstatSync,readdirSync} from 'node:fs';
import {rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join,resolve,dirname,basename} from 'node:path';
import {execFileSync} from 'node:child_process';
const key='PASSPORT_PUBLIC_WEBKIT_TEST_ROOT',prefix='passport-webkit-qualification-',marker='.owned-webkit-test-data';
function owned(root:string){if(dirname(resolve(root))!==resolve(tmpdir())||!basename(root).startsWith(prefix)||lstatSync(root).isSymbolicLink()||readFileSync(join(root,marker),'utf8')!=='temporary synthetic browser data\n')throw Error('Unowned WebKit data');return root;}
export function webkitLaunchOptions(){
 if(process.platform!=='darwin')return undefined;
 let root=process.env[key];if(!root){root=mkdtempSync(join(tmpdir(),prefix));writeFileSync(join(root,marker),'temporary synthetic browser data\n');process.env[key]=root;}owned(root);
 const prefs=join(root,'Library/Preferences');mkdirSync(prefs,{recursive:true});writeFileSync(join(prefs,'org.webkit.Playwright.plist'),'<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>NSAutomaticWindowAnimationsEnabled</key><false/></dict></plist>');
 return {env:{...process.env,CFFIXED_USER_HOME:root}};
}
export default async function cleanup(){const root=process.env[key];if(!root)return;owned(root);const clear=(p:string)=>{const s=lstatSync(p);if(s.isSymbolicLink())return;if(s.uid!==process.getuid?.())throw Error('Unowned temporary data');execFileSync('/bin/chmod',['-N',p]);if(s.isDirectory())for(const n of readdirSync(p))clear(join(p,n));};clear(root);await rm(root,{recursive:true,force:true,maxRetries:10,retryDelay:100});delete process.env[key];}
