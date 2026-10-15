import {readFileSync,lstatSync} from 'node:fs';
import {createHash} from 'node:crypto';
import {fileURLToPath} from 'node:url';
import {resolve,sep} from 'node:path';
const root=resolve(fileURLToPath(new URL('../',import.meta.url)));
const payload=JSON.parse(readFileSync(new URL('./payload.json',import.meta.url),'utf8'));
for(const [rel,hash]of Object.entries({...payload.assets,...payload.test_files})){
 const p=resolve(root,rel);if(!p.startsWith(root+sep)||!lstatSync(p).isFile()||lstatSync(p).isSymbolicLink())throw Error('Non-regular qualification input');
 if(createHash('sha256').update(readFileSync(p)).digest('hex')!==hash)throw Error('Qualification input changed: '+rel);
}
if(!readFileSync(resolve(root,'internal/webdist/bundle/workbook/index.html'),'utf8').includes(payload.source_ref))throw Error('Source provenance differs');
const routes=JSON.parse(readFileSync(resolve(root,'internal/webdist/bundle/data/routes.json'),'utf8'));
if(routes.curriculum_version!==payload.curriculum_version)throw Error('Curriculum differs');
console.log(JSON.stringify({source_ref:payload.source_ref,assets:Object.keys(payload.assets).length,test_files:Object.keys(payload.test_files).length,private_data:false}));
