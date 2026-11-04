// Real page + synthetic API. No credentials, live gateway or persistent browser profile.
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const { test } = require('node:test');
const { chromium, webkit } = require('playwright');
const { reach, watchSettingsReads, settingsDrawn } = require('./reader.cjs');
const assets=path.resolve(__dirname,'../assets');
for (const engine of (process.env.BROWSER?[process.env.BROWSER]:['chromium','webkit'])) {
 for (const lang of ['en','zh','zh-TW','ja','de']) {
 const labels={
  en:{open:'Manage local dictionary',save:'Save',cancel:'Cancel',reveal:'Reveal entries',hide:'Hide entries',reload:'Reload'},
  zh:{open:'管理本地词库',save:'保存',cancel:'取消',reveal:'显示词条',hide:'隐藏词条',reload:'重新加载'},
  'zh-TW':{open:'管理本機詞庫',save:'儲存',cancel:'取消',reveal:'顯示詞條',hide:'隱藏詞條',reload:'重新載入'},
  ja:{open:'ローカル辞書を管理',save:'保存',cancel:'キャンセル',reveal:'項目を表示',hide:'項目を隠す',reload:'再読み込み'},
  de:{open:'Lokales Wörterbuch verwalten',save:'Speichern',cancel:'Abbrechen',reveal:'Einträge anzeigen',hide:'Einträge verbergen',reload:'Neu laden'},
 }[lang];
 test(`${engine} ${lang}: local dictionary explicit save, conflict, reveal, cancellation`,{timeout:60000},async(t)=>{
  const browser=await(engine==='webkit'?webkit.launch():chromium.launch({channel:'chromium'}));t.after(()=>browser.close());
  const page=await browser.newPage({viewport:{width:420,height:760}});page.setDefaultTimeout(5000);
  let revision='new',words=[],writes=0,conflict=false,reads=0,holdSave=false,releaseSave;
  let saveStartedResolve;const saveStarted=new Promise(resolve=>{saveStartedResolve=resolve;});
  let saveFinishedResolve;const saveFinished=new Promise(resolve=>{saveFinishedResolve=resolve;});
  t.after(()=>releaseSave?.());
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
     if(holdSave&&b.content==='synthetic-save-in-flight'){saveStartedResolve();await new Promise(resolve=>{releaseSave=resolve;});}
    }
    const out={revision,count:words.length,bytes:Buffer.byteLength(words.join(''))};
    if(u.searchParams.get('reveal')==='1')out.words=words.slice(Number(u.searchParams.get('offset')||0),Number(u.searchParams.get('offset')||0)+100);
    if(holdSave&&r.request().method()==='POST'){
     try{return await json(out);}finally{saveFinishedResolve();}
    }
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
  await watchSettingsReads(page);
  await page.goto('http://magpie.test/?view=settings&tab=privacy');await settingsDrawn(page);
  const open=async()=>{const b=page.getByRole('button',{name:labels.open,exact:true});await reach(page,b);await b.click();};
  await open();const d=page.locator('dialog.local-dictionary');await d.getByRole('button',{name:labels.save,exact:true}).waitFor();
  await d.locator('textarea').fill('unsaved synthetic');await d.getByRole('button',{name:labels.cancel,exact:true}).click();assert.equal(writes,0);
  await open();await d.locator('textarea').fill('synthetic,password\n保密词');await d.getByRole('button',{name:labels.save,exact:true}).click();await page.waitForFunction(()=>document.querySelector('dialog .dictionary-buttons button')?.disabled===false);assert.equal(writes,1);assert.deepEqual(words,['synthetic,password','保密词']);
  assert.equal(await d.locator('pre').isVisible(),false);
  await d.getByRole('button',{name:labels.reveal,exact:true}).click();await d.locator('pre').waitFor();assert.match(await d.locator('pre').textContent(),/synthetic,password/);
  const beforeHide=reads;await d.getByRole('button',{name:labels.hide,exact:true}).click();assert.equal(await d.locator('pre').isVisible(),false);assert.equal(await d.locator('pre').textContent(),'');assert.equal(reads,beforeHide);
  await d.locator('textarea').fill('do not overwrite');conflict=true;await d.getByRole('button',{name:labels.save,exact:true}).click();await d.getByRole('alert').filter({hasText:'dictionary changed'}).waitFor();assert.equal(await d.locator('textarea').inputValue(),'do not overwrite');assert.deepEqual(words,['synthetic,password','保密词']);
  // Revealing or paging a newer generation must not silently rebase a draft.
  conflict=false;
  const bulk=Array.from({length:205},(_,i)=>`synthetic-page-${i}`);
  words=[...bulk];revision='other-window';
  await d.locator('[role="radio"][data-value="replace"]').click();
  await d.getByRole('button',{name:labels.reveal,exact:true}).click();await d.locator('pre').waitFor();
  const next=d.getByRole('button',{name:await page.evaluate(()=>t('Next')),exact:true});
  await next.click();await page.waitForFunction(()=>!document.querySelector('dialog textarea').disabled);
  assert.match(await d.locator('pre').textContent(),/^101: "synthetic-page-100"/);
  await d.getByRole('button',{name:labels.save,exact:true}).click();await page.waitForFunction(()=>!document.querySelector('dialog textarea').disabled);
  assert.deepEqual(words,bulk,'paging rebased a stale replacement and lost another writer\'s entries');
  assert.match(await d.getByRole('alert').textContent(),/dictionary changed/);assert.equal(await d.locator('textarea').inputValue(),'do not overwrite');
  await d.getByRole('button',{name:labels.reload,exact:true}).click();await page.waitForFunction(()=>!document.querySelector('dialog textarea').disabled);
  await d.getByRole('button',{name:labels.save,exact:true}).click();await page.waitForFunction(()=>!document.querySelector('dialog textarea').disabled);
  assert.deepEqual(words,['do not overwrite']);
  // Import all entries, reveal only one page, then append without replacing.
  bulk[0]=' literal, spaces ';bulk[1]='保密🔑';bulk[2]='literal\nnewline';
  await d.locator('input[type="file"]').setInputFiles({name:'synthetic.json',mimeType:'application/json',buffer:Buffer.from(JSON.stringify(bulk))});
  await page.waitForFunction(()=>!document.querySelector('dialog textarea').disabled);
  assert.equal(await d.locator('[role="radio"][data-value="json"]').getAttribute('aria-checked'),'true');
  await d.getByRole('button',{name:labels.save,exact:true}).click();await page.waitForFunction(()=>!document.querySelector('dialog textarea').disabled);
  assert.deepEqual(words,bulk);
  await d.getByRole('button',{name:labels.reveal,exact:true}).click();await d.locator('pre').waitFor();
  assert.equal((await d.locator('pre').textContent()).split('\n').length,100);
  await next.click();await page.waitForFunction(()=>!document.querySelector('dialog textarea').disabled);
  await next.click();await page.waitForFunction(()=>!document.querySelector('dialog textarea').disabled);
  assert.equal((await d.locator('pre').textContent()).split('\n').length,5);assert.equal(await next.isDisabled(),true);
  assert.equal(await d.evaluate(e=>e.scrollWidth<=e.clientWidth),true,'dialog overflows at 420px');
  await d.locator('[role="radio"][data-value="append"]').click();await d.locator('[role="radio"][data-value="text"]').click();
  await d.locator('textarea').fill('synthetic-added\nsynthetic-added');await d.getByRole('button',{name:labels.save,exact:true}).click();
  await page.waitForFunction(()=>!document.querySelector('dialog textarea').disabled);assert.deepEqual(words,[...bulk,'synthetic-added']);
  // Hold an already committed save's reply. Cancel cannot undo the commit,
  // and that old callback must not clear a newly opened editor's draft.
  holdSave=true;await d.locator('textarea').fill('synthetic-save-in-flight');await d.getByRole('button',{name:labels.save,exact:true}).click();
  await saveStarted;assert.equal(words.at(-1),'synthetic-save-in-flight');
  await d.getByRole('button',{name:labels.cancel,exact:true}).click();await d.waitFor({state:'detached'});
  await open();await d.locator('textarea').fill('new unsaved draft');
  releaseSave();await saveFinished;
  assert.equal(await d.locator('textarea').inputValue(),'new unsaved draft');
  await d.getByRole('button',{name:labels.reveal,exact:true}).click();await d.locator('pre').waitFor();
  assert.match(await d.locator('pre').textContent(),/literal, spaces/);
  const stored=await page.evaluate(()=>[...Object.values(localStorage),...Object.values(sessionStorage)].join('\n'));
  assert.equal(stored.includes('synthetic-save-in-flight'),false);
  await page.keyboard.press('Escape');await d.waitFor({state:'detached'});assert.deepEqual(errors,[]);
 });
}
}
