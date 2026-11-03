// Real page + synthetic API. No credentials, live gateway or persistent browser profile.
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const { test } = require('node:test');
const { chromium, webkit } = require('playwright');
const assets=path.resolve(__dirname,'../assets');
for (const engine of (process.env.BROWSER?[process.env.BROWSER]:['chromium','webkit'])) {
 for (const lang of ['en','zh']) {
 const labels=lang==='zh'?{open:'管理本地词库',save:'保存',cancel:'取消',reveal:'显示词条',hide:'隐藏词条'}:{open:'Manage local dictionary',save:'Save',cancel:'Cancel',reveal:'Reveal entries',hide:'Hide entries'};
 test(`${engine} ${lang}: local dictionary explicit save, conflict, reveal, cancellation`,async(t)=>{
  const browser=await(engine==='webkit'?webkit.launch():chromium.launch({channel:'chromium'}));t.after(()=>browser.close());
  const page=await browser.newPage({viewport:{width:640,height:1000}});page.setDefaultTimeout(5000);
  let revision='new',words=[],writes=0,conflict=false,reads=0;
  const errors=[];page.on('pageerror',e=>errors.push(e.message));
  await page.route('**/*',async r=>{
   const u=new URL(r.request().url()),p=u.pathname,json=x=>r.fulfill({json:x});
   if(p==='/boot.js')return r.fulfill({contentType:'text/javascript',body:`window.bootPrefs={lang:${JSON.stringify(lang)},theme:"light",web:true};`});
   if(p==='/wails/runtime.js')return r.fulfill({contentType:'text/javascript',body:'export const Window={};'});
   if(p==='/api/settings/local-dictionary'){
    if(r.request().method()==='GET')reads++;
    if(r.request().method()==='POST'){
     const b=JSON.parse(r.request().postData());writes++;
     if(conflict||b.revision!==revision)return r.fulfill({status:409,json:{error:'dictionary changed; reload before saving'}});
     const incoming=b.format==='json'?JSON.parse(b.content):b.content.replace(/^\uFEFF/,'').replace(/\r\n/g,'\n').split('\n').filter(Boolean);
     words=[...new Set((b.mode==='append'?words:[]).concat(incoming))];revision='revision-'+writes;
    }
    const out={revision,count:words.length,bytes:Buffer.byteLength(words.join(''))};
    if(u.searchParams.get('reveal')==='1')out.words=words.slice(Number(u.searchParams.get('offset')||0),Number(u.searchParams.get('offset')||0)+100);
    return json(out);
   }
   if(p==='/api/settings')return json({theme:'light',lang,redactWords:[]});
   if(p==='/api/state')return json({agents:[],profiles:[],settings:{theme:'light',lang}});
   if(p==='/api/providers')return json({providers:[],presets:[],excluded:[],gateway:{running:true,window:true,url:'http://127.0.0.1:3999'}});
   if(p==='/api/groups')return json({models:[],groups:[],pools:[]});
   if(p.startsWith('/api/'))return json({});
   const file=path.join(assets,p==='/'?'index.html':p);
   const contentType={'.html':'text/html','.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml','.png':'image/png'}[path.extname(file)];
   return r.fulfill({body:await fs.readFile(file),contentType});
  });
  await page.goto('http://magpie.test/?view=settings&tab=privacy');
  const open=()=>page.getByRole('button',{name:labels.open,exact:true}).click();
  await open();const d=page.locator('dialog.local-dictionary');await d.getByRole('button',{name:labels.save,exact:true}).waitFor();
  await d.locator('textarea').fill('unsaved synthetic');await d.getByRole('button',{name:labels.cancel,exact:true}).click();assert.equal(writes,0);
  await open();await d.locator('textarea').fill('synthetic,password\n保密词');await d.getByRole('button',{name:labels.save,exact:true}).click();await page.waitForFunction(()=>document.querySelector('dialog .dictionary-buttons button')?.disabled===false);assert.equal(writes,1);assert.deepEqual(words,['synthetic,password','保密词']);
  assert.equal(await d.locator('pre').isVisible(),false);
  await d.getByRole('button',{name:labels.reveal,exact:true}).click();await d.locator('pre').waitFor();assert.match(await d.locator('pre').textContent(),/synthetic,password/);
  const beforeHide=reads;await d.getByRole('button',{name:labels.hide,exact:true}).click();assert.equal(await d.locator('pre').isVisible(),false);assert.equal(await d.locator('pre').textContent(),'');assert.equal(reads,beforeHide);
  await d.locator('textarea').fill('do not overwrite');conflict=true;await d.getByRole('button',{name:labels.save,exact:true}).click();await d.getByRole('alert').filter({hasText:'dictionary changed'}).waitFor();assert.equal(await d.locator('textarea').inputValue(),'do not overwrite');assert.deepEqual(words,['synthetic,password','保密词']);
  await page.keyboard.press('Escape');await d.waitFor({state:'detached'});assert.deepEqual(errors,[]);
 });
}
}
