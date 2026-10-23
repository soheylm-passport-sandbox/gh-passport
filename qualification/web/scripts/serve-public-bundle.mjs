import '../../verify.mjs';
import {createServer} from 'node:http';
import {readFileSync,statSync} from 'node:fs';
import {resolve,extname,sep} from 'node:path';
import {fileURLToPath} from 'node:url';
const root=resolve(fileURLToPath(new URL('../../../internal/webdist/bundle/',import.meta.url)));
const mime={'.html':'text/html; charset=utf-8','.js':'text/javascript; charset=utf-8','.css':'text/css; charset=utf-8','.json':'application/json','.svg':'image/svg+xml','.png':'image/png','.jpg':'image/jpeg','.jpeg':'image/jpeg','.ico':'image/x-icon','.woff':'font/woff','.woff2':'font/woff2','.webp':'image/webp'};
const server=createServer((req,res)=>{
 try {
  const url=new URL(req.url,'http://127.0.0.1:4321');let path=resolve(root,'.'+decodeURIComponent(url.pathname));
  if(path!==root&&!path.startsWith(root+sep)){res.writeHead(400);res.end();return;}
  try {if(statSync(path).isDirectory())path=resolve(path,'index.html');}catch{}
  let status=200,body;try{body=readFileSync(path);}catch{status=404;path=resolve(root,'404.html');body=readFileSync(path);}
  res.writeHead(status,{'Content-Type':mime[extname(path)]||'application/octet-stream','Content-Length':body.length,'Cache-Control':'no-store'});res.end(body);
 }catch {res.writeHead(500);res.end();}
});
server.listen(Number(process.env.PASSPORT_PUBLIC_TEST_PORT || '4321'),'127.0.0.1');
for(const signal of ['SIGINT','SIGTERM'])process.on(signal,()=>server.close(()=>process.exit(0)));
