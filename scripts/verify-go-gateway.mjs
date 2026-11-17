// Real web and Go receiver behind an ephemeral Ozal-contract gateway fixture.
// This checks integration contracts; it does not exercise a deployed Ozal instance.
import { spawn, execFileSync } from 'node:child_process';
import { mkdtemp, writeFile, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { createServer, request } from 'node:http';
import { createServer as createSecureServer } from 'node:https';
import { readFileSync } from 'node:fs';
import { generateKeyPairSync, createHash, randomUUID, sign } from 'node:crypto';
import { chromium, expect } from '../web/node_modules/@playwright/test/index.mjs';
const root=resolve(import.meta.dirname,'..'), temp=await mkdtemp(join(tmpdir(),'custodia-gateway-'));
const binary=join(temp,'custodia'), data=join(temp,'data'), web=join(temp,'web');
const issuer='https://login.acme.example', audience='custodia';
const {publicKey,privateKey}=generateKeyPairSync('ed25519');
const kid=createHash('sha256').update(Buffer.from(publicKey.export({format:'jwk'}).x,'base64url')).digest('hex').slice(0,32);
const sessions=new Map(); let backend, browser, proxy, tenantFixture, backendPort, lastAssertion;
const tenantToken='gateway-tenant-token';
process.env.CUSTODIA_GATEWAY_TENANT_TOKEN=tenantToken;
function assertion(subject){
 const now=Math.floor(Date.now()/1000);
 const encode=value=>Buffer.from(JSON.stringify(value)).toString('base64url');
 const unsigned=`${encode({alg:'EdDSA',kid})}.${encode({iss:issuer,aud:audience,sub:subject,email:'shared@acme.example',name:subject,groups:['admin'],iat:now,exp:now+60,jti:randomUUID()})}`;
 return `${unsigned}.${sign(null,Buffer.from(unsigned),privateKey).toString('base64url')}`;
}
async function listen(server){await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve)); return server.address().port;}
async function stop(){if(backend?.exitCode===null && backend.signalCode===null){const done=new Promise(resolve=>backend.once('exit',resolve));backend.kill('SIGTERM');await done;}}
try{
 execFileSync('npm',['--prefix','web','run','build','--','--outDir',web],{cwd:root,env:{...process.env,VITE_FULL:'1'},stdio:'inherit'});
 execFileSync('go',['build','-o',binary,'./cmd/custodia'],{cwd:join(root,'server'),env:{...process.env,CGO_ENABLED:'0'},stdio:'inherit'});
 const empty=join(temp,'empty');
 execFileSync(binary,['init','--dir',empty],{stdio:'pipe'});
 let hasToken=true;try{await readFile(join(empty,'lab.token'));}catch(error){if(error.code!=='ENOENT')throw error;hasToken=false;}
 if(hasToken)throw Error('Real init created a laboratory credential');
 // Only this test database receives public synthetic inventory.
 execFileSync(binary,['init-lab','--dir',data],{stdio:'pipe'});
 const probe=createServer(); backendPort=await listen(probe);await new Promise(resolve=>probe.close(resolve));
 proxy=createServer(async(req,res)=>{
  try{
   if(req.url==='/fixture/login'){
    res.setHeader('Content-Type','text/html');res.end('<form method="post" action="/fixture/sign-in"><button name="subject" value="opaque-admin">Administrador de prueba</button><button name="subject" value="opaque-reader">Lector de prueba</button></form>');return;
   }
   if(req.url==='/fixture/sign-in' && req.method==='POST'){
    let body='';for await(const chunk of req)body+=chunk;
    const subject=new URLSearchParams(body).get('subject');if(!['opaque-admin','opaque-reader'].includes(subject)){res.writeHead(400).end();return;}
    const session=randomUUID();sessions.set(session,subject);res.setHeader('Set-Cookie',`fixture=${session}; HttpOnly; SameSite=Lax; Path=/`);res.writeHead(303,{Location:'/chains/person?lang=es'}).end();return;
   }
   if(req.url==='/_/auth/logout' && req.method==='POST'){
    const session=/fixture=([^;]+)/.exec(req.headers.cookie??'')?.[1];sessions.delete(session);
    res.setHeader('Set-Cookie','fixture=; HttpOnly; SameSite=Lax; Path=/; Max-Age=0');res.writeHead(303,{Location:'/fixture/login'}).end();return;
   }
   const subject=sessions.get(/fixture=([^;]+)/.exec(req.headers.cookie??'')?.[1]);
   if(!subject){res.writeHead(302,{Location:'/fixture/login'}).end();return;}
   const headers={...req.headers};for(const key of Object.keys(headers))if(key.toLowerCase().replaceAll('_','-')==='x-ozal-assertion')delete headers[key];
   lastAssertion=assertion(subject);headers['x-ozal-assertion']=lastAssertion;
   const upstream=request({hostname:'127.0.0.1',port:backendPort,path:req.url,method:req.method,headers},response=>{res.writeHead(response.statusCode,response.headers);response.pipe(res);});
   upstream.on('error',()=>res.writeHead(502).end());req.pipe(upstream);
  }catch{res.writeHead(500).end();}
 });
 const proxyPort=await listen(proxy), base=`http://127.0.0.1:${proxyPort}`, direct=`http://127.0.0.1:${backendPort}`;
 const config=join(temp,'gateway.json'), key=join(temp,'public.pem');
 await writeFile(key,publicKey.export({format:'pem',type:'spki'}));
 await writeFile(config,JSON.stringify({issuer,audience,public_origin:base,public_key_files:[key]}));
 execFileSync(binary,['bootstrap-admin','--dir',data,'--gateway-config',config,'--subject','opaque-admin','--connections','acme-identity,acme-netskope'],{stdio:'pipe'});
 async function start(){
  backend=spawn(binary,['serve','--auth-mode','ozal','--dir',data,'--gateway-config',config,'--web-dir',web,'--listen',`127.0.0.1:${backendPort}`],{stdio:['ignore','pipe','pipe']});
  await new Promise((resolve,reject)=>{const timer=setTimeout(()=>reject(Error('Backend startup timed out')),10000);backend.stdout.once('data',()=>{clearTimeout(timer);resolve();});backend.once('exit',code=>{clearTimeout(timer);reject(Error(`Backend exited ${code}`));});});
 }
 // A tenant PAM fixture over TLS: the administrator connects it from the UI
 // with an env: credential reference and a local CA, then collects it.
 const tenantKey=join(temp,'tenant.key'),tenantCert=join(temp,'tenant.crt');
 execFileSync('openssl',['req','-x509','-newkey','rsa:2048','-nodes','-keyout',tenantKey,'-out',tenantCert,'-days','1','-subj','/CN=127.0.0.1','-addext','subjectAltName=IP:127.0.0.1'],{stdio:'pipe'});
 tenantFixture=createSecureServer({key:readFileSync(tenantKey),cert:readFileSync(tenantCert)},(req,res)=>{
  if(req.headers.authorization!==`Bearer ${tenantToken}`){res.writeHead(401).end();return;}
  if(req.url?.startsWith('/API/Safes')){
   res.writeHead(200,{'Content-Type':'application/json'});
   res.end(JSON.stringify({value:[{safeNumber:7001,safeName:'Tenant Alpha',safeUrlId:'tenant-alpha'},{safeNumber:7002,safeName:'Tenant Beta',safeUrlId:'tenant-beta'}],count:2,nextLink:''}));
   return;
  }
  if(req.url?.startsWith('/API/Accounts')){
   res.writeHead(200,{'Content-Type':'application/json'});
   res.end(JSON.stringify({value:[{id:'acct-1',name:'svc-alpha',userName:'svc-alpha',safeName:'Tenant Alpha',platformId:'Unix'},{id:'acct-2',name:'svc-beta',userName:'svc-beta',safeName:'Tenant Beta',platformId:'Windows'}],count:2,nextLink:''}));
   return;
  }
  res.writeHead(404).end();
 });
 const tenantPort=await listen(tenantFixture), tenantURL=`https://127.0.0.1:${tenantPort}`;
 await start();browser=await chromium.launch();
 const admin=await browser.newContext({locale:'es-AR'}), reader=await browser.newContext({locale:'es-AR'});
 const page=await admin.newPage(), limited=await reader.newPage();
 await limited.goto(`${base}/fixture/login`);await limited.getByRole('button',{name:'Lector de prueba'}).click();
 if((await reader.request.get(`${base}/api/auth/status`)).status()!==403)throw Error('Unregistered identity was provisioned automatically');
 await page.goto(`${base}/fixture/login`);await page.getByRole('button',{name:'Administrador de prueba'}).click();
 await expect(page.getByRole('heading',{name:'Personas',exact:true})).toBeVisible();
 const mutate=(method,body)=>page.evaluate(async({method,body})=>(await fetch('/api/access/readers',{method,headers:{'Content-Type':'application/json'},body:JSON.stringify(body)})).status,{method,body});
 if(await mutate('POST',{subject:'opaque-reader',connections:['acme-identity']})!==204)throw Error('Admin could not grant scoped access');
 await limited.goto(`${base}/chains/person?lang=es`);await expect(limited.getByRole('heading',{name:'Personas',exact:true})).toBeVisible();
 await limited.getByRole('searchbox',{name:'Buscar persona por correo o nombre'}).fill('alice');await limited.getByRole('option',{name:/Alice Morgan/}).click();
 const summary=await (await reader.request.get(`${base}/api/chains/person?email=alice.morgan@acme.example`)).json();
 if(!summary.tools.identity||summary.tools.netskope)throw Error('Reader scope leaked a source');
 if((await reader.request.get(`${base}/api/chains/person/coverage`)).status()!==403)throw Error('Reader obtained forbidden cross-source inference');
 const status=await (await admin.request.get(`${base}/api/auth/status`)).json();
 if(!JSON.stringify(status).includes('Administrador'))throw Error('Bootstrap administrator not recognized');
 const replay=lastAssertion;
 if((await fetch(`${direct}/api/auth/status`,{headers:{'X-Ozal-Assertion':replay}})).status!==401)throw Error('Assertion replay accepted');
 await stop();await start();
 if((await fetch(`${direct}/api/auth/status`,{headers:{'X-Ozal-Assertion':replay}})).status!==401)throw Error('Restart lost replay protection');
 const token=await readFile(join(data,'lab.token'),'utf8');
 if((await fetch(`${direct}/api/auth/status`,{headers:{Authorization:`Bearer ${token}`,'X-Auth-Request-User':'opaque-admin'}})).status!==401)throw Error('Real mode accepted lab/header fallback');
 if(await mutate('DELETE',{subject:'opaque-reader'})!==204)throw Error('Reader revocation failed');
 if((await reader.request.get(`${base}/api/auth/status`)).status()!==403)throw Error('Revoked reader retained access');
 // Onboarding from an empty catalog: choose tool, tenant URL, credential reference.
 await page.getByRole('button',{name:'Abrir menú de usuario',exact:true}).click();
 await page.getByRole('button',{name:'Actividad',exact:true}).click();
 await page.getByRole('tab',{name:'Conexiones'}).click();
 await page.getByLabel('Herramienta',{exact:true}).selectOption('cyberark-pam-safes-v1');
 await page.getByLabel('URL del tenant',{exact:true}).fill(tenantURL);
 await page.getByRole('checkbox',{name:'accounts'}).check();
 await page.getByLabel('Alcance',{exact:true}).fill('gateway-tenant-multi-v1');
 await page.getByLabel('Referencia de credencial',{exact:true}).fill('env:CUSTODIA_GATEWAY_TENANT_TOKEN');
 await page.getByLabel('CA adicional',{exact:true}).fill(tenantCert);
 await page.getByRole('button',{name:'Conectar tenant'}).click();
 await expect(page.getByText(/Conexión .* registrada para safes, accounts/)).toBeVisible();
 await page.getByRole('button',{name:'Probar acceso'}).click();
 await expect(page.getByText('Acceso correcto')).toBeVisible();
 await expect(page.locator('.connection-probe-table')).toContainText('/API/Safes');
 await expect(page.locator('.connection-probe-table')).toContainText('/API/Accounts');
 const schedules=await (await admin.request.get(`${base}/api/ingestion/schedules`)).json();
 const tenantRows=schedules.rows.filter(row=>String(row.source_file).startsWith('cyberark-pam-safes:'));
 if(tenantRows.length!==2)throw Error(`Onboarded connection must expose two collection rows, got ${tenantRows.length}`);
 const queueResponse=await admin.request.post(`${base}/api/ingestion/all/runs?scope=cyberark`,{data:{},headers:{Origin:base}});
 const queuedText=await queueResponse.text();
 if(queueResponse.status()!==202)throw Error(`Tenant batch returned ${queueResponse.status()}: ${queuedText}`);
 const queued=JSON.parse(queuedText);
 if(!Array.isArray(queued.runs)||queued.runs.length!==2)throw Error(`Tenant group was not queued per collection: ${queuedText}`);
 const queuedKeys=new Set(queued.runs.map(run=>run.task_key));
 for(const row of tenantRows)if(!queuedKeys.has(row.task_key))throw Error(`Collection ${row.task_key} was not queued`);
 for(const queuedRun of queued.runs){
  let tenantRun;
  for(let attempt=0;attempt<100;attempt+=1){
   tenantRun=await (await admin.request.get(`${base}/api/ingestion/runs/${queuedRun.run_id}`)).json();
   if(!['queued','running'].includes(tenantRun.status))break;
   await new Promise(resolve=>setTimeout(resolve,100));
  }
  if(tenantRun.status!=='completed'||tenantRun.received_object_count!==2)throw Error(`Collection ${tenantRun.collection} did not complete`);
 }
 const safesRow=tenantRows.find(row=>row.task_key.endsWith('safes'));
 const accountsRow=tenantRows.find(row=>row.task_key.endsWith('accounts'));
 const safesRecords=await (await admin.request.get(`${base}/api/ingestion/connections/${safesRow.task_key}/records`)).json();
 const accountRecords=await (await admin.request.get(`${base}/api/ingestion/connections/${accountsRow.task_key}/records`)).json();
 if(safesRecords.rows.length!==2||safesRecords.rows[0].attributes.safe_url_id!=='tenant-alpha')throw Error('Safes records missing after onboarding');
 if(accountRecords.rows.length!==2||accountRecords.rows[0].attributes.account_id!=='acct-1')throw Error('Account records missing after onboarding');
 // Exercise the frontend logout function, including the browser form submitted to Ozal.
 await page.getByRole('button',{name:'Abrir menú de usuario',exact:true}).click();
 await page.getByRole('button',{name:'Cerrar sesión',exact:true}).click();
 await expect(page).toHaveURL(`${base}/fixture/login`);
 if(sessions.size!==1)throw Error('Gateway logout did not remove administrator session');
 console.log('Ozal-contract gateway + Go + SQLite + real web: signed entry, explicit bootstrap, scoped reader, replay, tenant onboarding from the UI (tool, TLS tenant, credential reference, two collections), probe, collection, revocation and gateway logout OK.');
}finally{
 await browser?.close();await stop();if(tenantFixture)await new Promise(resolve=>tenantFixture.close(resolve));if(proxy)await new Promise(resolve=>proxy.close(resolve));await rm(temp,{recursive:true,force:true});
}
