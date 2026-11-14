// Real page + synthetic API. No credentials, live gateway or persistent browser profile.
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const { test } = require('node:test');
const { chromium, webkit } = require('playwright');
const { reach, watchSettingsReads, settingsDrawn } = require('./reader.cjs');
const assets=path.resolve(process.env.MAGPIE_DICTIONARY_ASSETS||path.join(__dirname,'../assets'));
for (const engine of (process.env.BROWSER?[process.env.BROWSER]:['chromium','webkit'])) {
 for (const lang of ['en','zh','zh-TW','ja','de']) {
 const labels={
  en:{open:'Manage local dictionary',save:'Save',cancel:'Cancel',view:'View saved entries',hide:'Hide entries',reload:'Reload'},
  zh:{open:'管理本地词库',save:'保存',cancel:'取消',view:'查看已有内容',hide:'隐藏已有内容',reload:'重新加载'},
  'zh-TW':{open:'管理本機詞庫',save:'儲存',cancel:'取消',view:'查看已有內容',hide:'隱藏已有內容',reload:'重新載入'},
  ja:{open:'ローカル辞書を管理',save:'保存',cancel:'キャンセル',view:'保存済みの項目を表示',hide:'項目を隠す',reload:'再読み込み'},
  de:{open:'Lokales Wörterbuch verwalten',save:'Speichern',cancel:'Abbrechen',view:'Gespeicherte Einträge anzeigen',hide:'Einträge ausblenden',reload:'Neu laden'},
 }[lang];
 test(`${engine} ${lang}: local dictionary explicit save, conflict, reveal, cancellation`,{timeout:60000},async(t)=>{
  const browser=await(engine==='webkit'?webkit.launch():chromium.launch({channel:'chromium'}));
  let page,releaseSave;
  t.after(async()=>{releaseSave?.();try{await page?.context().close();}finally{await browser.close();}});
  page=await browser.newPage({viewport:{width:420,height:760}});
  page.setDefaultTimeout(5000);
  page.setDefaultNavigationTimeout(60000);
  let revision='new',words=[],allowWords=[],writes=0,conflict=false,reads=0,holdSave=false;
  let saveStartedResolve;const saveStarted=new Promise(resolve=>{saveStartedResolve=resolve;});
  let saveFinishedResolve;const saveFinished=new Promise(resolve=>{saveFinishedResolve=resolve;});
  const errors=[],posts=[];page.on('pageerror',e=>errors.push(e.message));
  await page.route('**/*',async r=>{
   const u=new URL(r.request().url()),p=u.pathname,json=x=>r.fulfill({json:x});
   if(p==='/boot.js')return r.fulfill({contentType:'text/javascript',body:`window.bootPrefs={lang:${JSON.stringify(lang)},theme:"light",web:true};`});
   if(p==='/wails/runtime.js')return r.fulfill({contentType:'text/javascript',body:'export const Window={};'});
   if(p==='/api/settings/local-dictionary'){
    let target=u.searchParams.get('target')||'mask';
    if(r.request().method()==='GET')reads++;
    if(r.request().method()==='POST'){
     const b=JSON.parse(r.request().postData());posts.push(b);writes++;target=b.target;
     assert(['mask','allow'].includes(target),'every mutation names its list');
     if(conflict||b.revision!==revision)return r.fulfill({status:409,json:{error:'dictionary changed; reload before saving'}});
     const previous=target==='allow'?allowWords:words;
     let incoming;
     if(b.mode==='edit'){
      assert.equal(b.format,'json');assert.equal(b.content,'');assert(Array.isArray(b.words));
      assert(Number.isInteger(b.offset)&&b.offset>=0);assert(Number.isInteger(b.count)&&b.count>=1&&b.count<=100);
      assert(b.offset+b.count<=previous.length&&b.words.length<=100,'edits replace one saved page');
      incoming=previous.slice(0,b.offset).concat(b.words,previous.slice(b.offset+b.count));
     }else{
      assert(['append','replace'].includes(b.mode));
      incoming=b.format==='json'?JSON.parse(b.content):b.content.replace(/^\uFEFF/,'').replace(/\r\n/g,'\n').split('\n');
      if(b.mode==='append')incoming=previous.concat(incoming);
     }
     assert(incoming.every(word=>typeof word==='string'));
     const updated=[...new Set(incoming.filter(word=>word!==''))];
     if(target==='allow')allowWords=updated;else words=updated;
     revision='revision-'+writes;
     if(holdSave&&b.content==='synthetic-save-in-flight'){saveStartedResolve();await new Promise(resolve=>{releaseSave=resolve;});}
    }
    const selected=target==='allow'?allowWords:words;
    const out={revision,count:selected.length,bytes:Buffer.byteLength(selected.join(''))};
    if(u.searchParams.get('reveal')==='1'){
     const offset=Number(u.searchParams.get('offset')||0),limit=Math.min(100,Number(u.searchParams.get('limit')||100));
     out.words=selected.slice(offset,offset+limit);
    }
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
  const T=key=>page.evaluate(key=>t(key),key);
  const discard=await T('Discard');
  const d=page.locator('dialog.local-dictionary'),confirm=page.locator('dialog.action-confirm');
  const input=d.getByRole('textbox',{name:await T('Dictionary entries'),exact:true}),preview=d.locator('.dictionary-preview');
  const example=d.locator('.dictionary-example'),sample=example.locator('pre');
  const reload=d.getByRole('button',{name:labels.reload,exact:true}),save=d.getByRole('button',{name:labels.save,exact:true});
  const ready=()=>page.waitForFunction(()=>{const input=document.querySelector('dialog.local-dictionary > .dictionary-body > textarea');return input&&!input.disabled;});
  const accept=async action=>{await confirm.waitFor();await confirm.getByRole('button',{name:await T(action),exact:true}).click();await confirm.waitFor({state:'detached'});};
  const submit=async()=>{
   const replace=await d.locator('[role="radio"][data-value="replace"]').getAttribute('aria-checked')==='true',before=writes;
   await save.click();
   if(replace){await confirm.waitFor();assert.equal(writes,before,'replacement needs confirmation before POST');await accept('Replace all');}
  };
  await open();await save.waitFor();
  const centered=await d.evaluate((e)=>{const r=e.getBoundingClientRect();return {x:r.x,y:r.y,width:r.width,height:r.height,viewport:[innerWidth,innerHeight]};});
  assert(Math.abs(centered.x+centered.width/2-centered.viewport[0]/2)<=1,'dictionary dialog is centered horizontally');
  assert(Math.abs(centered.y+centered.height/2-centered.viewport[1]/2)<=1,'dictionary dialog is centered vertically');
  const backdrop=await d.evaluate((e)=>{const r=e.getBoundingClientRect();const x=Math.max(1,Math.min(innerWidth-1,r.right+Math.max(1,(innerWidth-r.right)/2)));const y=Math.max(1,Math.min(innerHeight-1,(r.top+r.bottom)/2));return {x,y,inside:x>r.left&&x<r.right&&y>r.top&&y<r.bottom};});
  assert.equal(backdrop.inside,false,'backdrop point is outside the dialog');
  await page.mouse.click(backdrop.x,backdrop.y);await d.waitFor({state:'detached'});
  await open();await ready();
  assert.equal(await sample.textContent(),'DEMO_SECRET_123\nINTERNAL_PROJECT','text example follows the selected mask list');
  await input.fill('unsaved synthetic');
  // An expanded example puts the field below the scrollable body. WebKit's
  // fill can focus it there; a reader wheels it into sight before dragging.
  for(let attempt=0;attempt<30;attempt++){
   const at=await input.evaluate(e=>{
    const r=e.getBoundingClientRect(),body=e.closest('.dictionary-body'),b=body.getBoundingClientRect();
    const hit=document.elementFromPoint(r.left+r.width/2,r.top+r.height/2);
    return {clear:r.top>=b.top&&r.bottom<=b.bottom&&hit===e,was:body.scrollTop,x:b.left+b.width/2,y:b.top+b.height/2,dy:r.top<b.top?-80:80};
   });
   if(at.clear)break;
   await page.mouse.move(at.x,at.y);await page.mouse.wheel(0,at.dy);
   await page.waitForFunction(was=>document.querySelector('dialog.local-dictionary .dictionary-body').scrollTop!==was,at.was,{timeout:1000});
   assert(attempt<29,'the import field can be wheeled fully into sight');
  }
  await input.click();
  await d.evaluate((e)=>{window.__dictionaryGesture=[];for(const kind of ['pointerdown','pointerup','click'])e.addEventListener(kind,(event)=>{const r=e.getBoundingClientRect();window.__dictionaryGesture.push({kind,trusted:event.isTrusted,outside:event.clientX<r.left||event.clientX>r.right||event.clientY<r.top||event.clientY>r.bottom});},true);});
  const field=await input.boundingBox();assert(field);
  await page.mouse.move(field.x+field.width/2,field.y+field.height/2);await page.mouse.down();await page.mouse.move(backdrop.x,backdrop.y,{steps:8});await page.mouse.up();
  assert.equal(await d.count(),1,'dragging a selection out must not dismiss the dictionary');
  assert.equal(await confirm.count(),0,'dragging a selection out must not ask to discard the draft');
  assert.equal(await input.inputValue(),'unsaved synthetic');
  const gesture=await page.evaluate(()=>window.__dictionaryGesture);
  assert(gesture.every(e=>e.trusted),'the backdrop regression uses trusted pointer events');
  assert(gesture.some(e=>e.kind==='pointerdown'&&!e.outside)&&gesture.some(e=>e.kind==='pointerup'&&e.outside),'the gesture starts inside and ends on the backdrop: '+JSON.stringify({field,backdrop,gesture,visible:await input.evaluate(e=>{const b=e.getBoundingClientRect(),p=e.parentElement.getBoundingClientRect(),hit=document.elementFromPoint(b.x+b.width/2,b.y+b.height/2);return {field:{top:b.top,bottom:b.bottom},body:{top:p.top,bottom:p.bottom},hit:hit?.tagName,hitClass:hit?.className};})}));
  await d.getByRole('button',{name:labels.cancel,exact:true}).click();await accept('Cancel');
  assert.equal(await input.inputValue(),'unsaved synthetic','canceling close keeps the draft');assert.equal(await d.count(),1);
  await d.getByRole('button',{name:labels.cancel,exact:true}).click();await accept('Discard');await d.waitFor({state:'detached'});assert.equal(writes,0);
  await open();await input.fill('synthetic,password\n保密词');await submit();await ready();assert.equal(writes,1);assert.deepEqual(words,['synthetic,password','保密词']);
  assert.equal(await preview.isVisible(),false);
  await d.getByRole('button',{name:labels.view,exact:true}).click();await preview.waitFor();assert.match(await preview.textContent(),/synthetic,password/);
  const readCodeFonts=()=>d.evaluate(box=>{
   const props=['fontFamily','fontSize','fontWeight','fontStyle','fontStretch','lineHeight'];
   const take=e=>Object.fromEntries(props.map(key=>[key,getComputedStyle(e)[key]]));
   return {input:take(box.querySelector('.dictionary-body > textarea')),preview:take(box.querySelector('.dictionary-preview')),example:take(box.querySelector('.dictionary-example pre'))};
  });
  const codeFonts=await readCodeFonts();
  assert.deepEqual(codeFonts.preview,codeFonts.input,'saved entries use the same code font and traits as their editor');
  assert.deepEqual(codeFonts.example,codeFonts.input,'examples use the same code font and traits as their editor');
  const beforeHide=reads;await d.getByRole('button',{name:labels.hide,exact:true}).click();assert.equal(await preview.isVisible(),false);assert.equal(await preview.textContent(),'');assert.equal(reads,beforeHide);
  await input.fill('do not overwrite');conflict=true;await submit();await d.getByRole('alert').filter({hasText:'dictionary changed'}).waitFor();assert.equal(await input.inputValue(),'do not overwrite');assert.deepEqual(words,['synthetic,password','保密词']);
  // Reload must ask before discarding a draft. Cancel keeps the draft, and
  // accepting the same confirmation is what establishes a fresh revision.
  const readsBeforeCancel=reads,writesBeforeCancel=writes;
  await reload.click();await confirm.waitFor();await confirm.getByRole('button',{name:await page.evaluate(()=>t('Cancel')),exact:true}).click();await confirm.waitFor({state:'detached'});assert.equal(await input.inputValue(),'do not overwrite');
  assert.equal(reads,readsBeforeCancel,'cancel does not establish a new revision');assert.equal(writes,writesBeforeCancel,'cancel sends no mutation');
  // Revealing or paging a newer generation must not silently rebase a draft.
  conflict=false;
  const draftRevision=revision;
  const bulk=Array.from({length:205},(_,i)=>`synthetic-page-${i}`);
  words=[...bulk];revision='other-window';
  await d.locator('[role="radio"][data-value="replace"]').click();
  assert.equal(await example.locator('p').first().textContent(),await T('Replace all replaces only Mask these literals; Never mask stays unchanged.'));
  await d.getByRole('button',{name:labels.view,exact:true}).click();await preview.waitFor();
  const next=d.getByRole('button',{name:await page.evaluate(()=>t('Next')),exact:true});
  await next.click();await ready();
  assert.match(await preview.textContent(),/^101: "synthetic-page-100"/);
  await submit();await ready();
  assert.equal(posts.at(-1).revision,draftRevision,'viewing and paging must not rebase an import draft');
  assert.deepEqual(words,bulk,'paging rebased a stale replacement and lost another writer\'s entries');
  assert.match(await d.getByRole('alert').textContent(),/dictionary changed/);assert.equal(await input.inputValue(),'do not overwrite');
  await reload.click();await confirm.waitFor();await confirm.getByRole('button',{name:discard,exact:true}).click();await confirm.waitFor({state:'detached'});await ready();
  assert.equal(await input.inputValue(),'','accepted reload discards the old draft');assert.deepEqual(words,bulk,'reload does not modify the saved dictionary');
  await input.fill('do not overwrite');
  const beforeReplaceCancel=writes;
  await save.click();await accept('Cancel');
  assert.equal(writes,beforeReplaceCancel,'canceling Replace all sends no mutation');assert.equal(await input.inputValue(),'do not overwrite');
  await submit();await ready();
  assert.deepEqual(words,['do not overwrite']);
  // Import all entries, reveal only one page, then append without replacing.
  bulk[0]=' literal, spaces ';bulk[1]='保密🔑';bulk[2]='literal\nnewline';
  await d.locator('input[type="file"]').setInputFiles({name:'synthetic.json',mimeType:'application/json',buffer:Buffer.from(JSON.stringify(bulk))});
  await ready();
  assert.equal(await d.locator('[role="radio"][data-value="json"]').getAttribute('aria-checked'),'true');
  assert.deepEqual(JSON.parse(await sample.textContent()),['DEMO_SECRET_123','INTERNAL_PROJECT'],'file format updates the example');
  // A selected file also makes Reload destructive, even if its text was cleared.
  await input.fill('');const selectedFile=await d.locator('input[type="file"]').inputValue(),beforeFileReload=reads;
  await reload.click();await confirm.waitFor();await confirm.getByRole('button',{name:await page.evaluate(()=>t('Cancel')),exact:true}).click();await confirm.waitFor({state:'detached'});
  assert.equal(await d.locator('input[type="file"]').inputValue(),selectedFile,'cancel keeps the selected file');assert.equal(reads,beforeFileReload);
  await reload.click();await confirm.waitFor();await confirm.getByRole('button',{name:discard,exact:true}).click();await confirm.waitFor({state:'detached'});await ready();
  assert.equal(await d.locator('input[type="file"]').inputValue(),'','accepted reload clears the selected file');assert.equal(await input.inputValue(),'');assert.deepEqual(words,['do not overwrite']);
  await d.locator('input[type="file"]').setInputFiles({name:'synthetic.json',mimeType:'application/json',buffer:Buffer.from(JSON.stringify(bulk))});
  await ready();
  await submit();await ready();
  assert.deepEqual(words,bulk);
  await d.getByRole('button',{name:labels.view,exact:true}).click();await preview.waitFor();
  assert.equal((await preview.textContent()).split('\n').length,100);
  await next.click();await ready();
  await next.click();await ready();
  assert.equal((await preview.textContent()).split('\n').length,5);assert.equal(await next.isDisabled(),true);
  assert.equal(await d.evaluate(e=>e.scrollWidth<=e.clientWidth),true,'dialog overflows at 420px');
  await d.locator('[role="radio"][data-value="append"]').click();await d.locator('[role="radio"][data-value="text"]').click();
  assert.equal(await sample.textContent(),'DEMO_SECRET_123\nINTERNAL_PROJECT');
  assert.equal(await example.locator('p').first().textContent(),await T('Append adds these entries to Mask these literals and keeps existing entries.'));
  await input.fill('synthetic-added\nsynthetic-added');await submit();
  await ready();assert.deepEqual(words,[...bulk,'synthetic-added']);
  // Hold an already committed save's reply. Cancel cannot undo the commit,
  // and that old callback must not clear a newly opened editor's draft.
  holdSave=true;await input.fill('synthetic-save-in-flight');await submit();
  await saveStarted;assert.equal(words.at(-1),'synthetic-save-in-flight');
  await d.getByRole('button',{name:labels.cancel,exact:true}).click();await d.waitFor({state:'detached'});
  await open();await input.fill('new unsaved draft');
  releaseSave();await saveFinished;holdSave=false;
  assert.equal(await input.inputValue(),'new unsaved draft');
  await d.getByRole('button',{name:labels.view,exact:true}).click();await preview.waitFor();
  assert.match(await preview.textContent(),/literal, spaces/);
  const stored=await page.evaluate(()=>[...Object.values(localStorage),...Object.values(sessionStorage)].join('\n'));
  assert.equal(stored.includes('synthetic-save-in-flight'),false);
  // Switching list is explicit about discarding a draft, and cannot replace
  // the other saved list. Both editors still share one saved revision.
  const maskBefore=[...words];
  const allow=d.locator('[role="radio"][data-value="allow"]');
  await allow.click();await confirm.waitFor();
  await confirm.getByRole('button',{name:await page.evaluate(()=>t('Cancel')),exact:true}).click();
  await confirm.waitFor({state:'detached'});
  assert.equal(await input.inputValue(),'new unsaved draft');
  await allow.click();await confirm.waitFor();
  await confirm.getByRole('button',{name:discard,exact:true}).click();
  await confirm.waitFor({state:'detached'});
  await ready();
  assert.equal(await allow.getAttribute('aria-checked'),'true');
  assert.equal(await sample.textContent(),'public@example.test\nPUBLIC_PROJECT','switching lists updates the example');
  assert.equal(await example.locator('p').first().textContent(),await T('Append adds these entries to Never mask and keeps existing entries.'));
  await input.fill('synthetic-always-visible');
  await submit();
  await ready();
  assert.deepEqual(allowWords,['synthetic-always-visible']);assert.deepEqual(words,maskBefore);
  await d.getByRole('button',{name:labels.view,exact:true}).click();
  await preview.waitFor();
  assert.match(await preview.textContent(),/synthetic-always-visible/);
  // The import revision stays at the metadata read. Editing instead uses the
  // viewed page's generation, including a concurrent save of the other list.
  await d.locator('[role="radio"][data-value="mask"]').click();await ready();
  const importRevision=revision;
  allowWords.push('synthetic-other-window-allow');revision='other-list-generation';
  await d.getByRole('button',{name:labels.view,exact:true}).click();await preview.waitFor();
  await next.click();await ready();
  const viewedRevision=revision,maskBeforePage=[...words],allowBeforePage=[...allowWords];
  assert.notEqual(viewedRevision,importRevision);
  const edit=d.getByRole('button',{name:await T('Edit this page'),exact:true});
  const saveEdits=d.getByRole('button',{name:await T('Save changes'),exact:true});
  const entries=d.locator('.dictionary-page-editor .dictionary-entry'),fields=entries.locator('textarea');
  await edit.click();await fields.first().waitFor();
  assert.equal(await fields.count(),100,'editing exposes at most the viewed page');
  await fields.nth(0).fill('synthetic-page-edited');
  await entries.nth(1).getByRole('button',{name:await T('Remove entry'),exact:true}).click();
  assert.equal(await fields.nth(1).isDisabled(),true);
  await entries.nth(1).getByRole('button',{name:await T('Undo'),exact:true}).click();
  assert.equal(await fields.nth(1).isDisabled(),false);assert.equal(await fields.nth(1).inputValue(),maskBeforePage[101]);
  await entries.nth(2).getByRole('button',{name:await T('Remove entry'),exact:true}).click();
  assert.deepEqual(words,maskBeforePage,'editing and removing rows do not save implicitly');
  const editedPage=maskBeforePage.slice(100,200);editedPage[0]='synthetic-page-edited';editedPage.splice(2,1);
  await saveEdits.click();await ready();
  assert.deepEqual(posts.at(-1),{revision:viewedRevision,mode:'edit',format:'json',content:'',target:'mask',offset:100,count:100,words:editedPage});
  assert.deepEqual(words,maskBeforePage.slice(0,100).concat(editedPage,maskBeforePage.slice(200)),'other pages survive a page edit');
  assert.deepEqual(allowWords,allowBeforePage,'a mask page edit keeps the other list');
  assert.match(await preview.textContent(),/^101: "synthetic-page-edited"/);
  // Another list changing after the preview also invalidates page edits;
  // the 409 must keep the unsaved row and both saved lists intact.
  const stalePageRevision=revision,maskBeforeConflict=[...words];
  await edit.click();await fields.first().fill('stale page draft');
  allowWords.push('synthetic-allow-after-preview');revision='after-page-view';
  const allowBeforeConflict=[...allowWords];
  await saveEdits.click();await d.getByRole('alert').filter({hasText:'dictionary changed'}).waitFor();
  assert.equal(posts.at(-1).revision,stalePageRevision);assert.equal(await fields.first().inputValue(),'stale page draft');
  assert.deepEqual(words,maskBeforeConflict);assert.deepEqual(allowWords,allowBeforeConflict);
  await d.getByRole('button',{name:await T('Discard edits'),exact:true}).click();await accept('Discard');await ready();
  const maskBeforeResize=[...words],allowBeforeResize=[...allowWords];
  const controls=await d.evaluate(e=>{
    const title=getComputedStyle(e.querySelector('h2')),action=getComputedStyle(e.querySelector('.dictionary-buttons button'));
    return {title:parseFloat(title.fontSize),weight:title.fontWeight,radius:parseFloat(action.borderRadius)};
  });
  assert(controls.title<=16&&Number(controls.weight)<=600,'dialog typography must use the application scale');
  assert(controls.radius>=5,'dialog actions must use the shared rounded controls');
  assert.equal(await d.locator(':scope > .surface-resize').isVisible(),true,'the whole dialog has its own resize control');
  await page.setViewportSize({width:1200,height:900});
  const desktopCodeFonts=await readCodeFonts();
  assert.deepEqual(desktopCodeFonts.preview,desktopCodeFonts.input,'desktop saved entries keep the chosen code font and traits');
  assert.deepEqual(desktopCodeFonts.example,desktopCodeFonts.input,'desktop examples keep the chosen code font and traits');
  assert.equal(desktopCodeFonts.input.fontSize,'12px','desktop code returns to the application scale after the mobile field size');
  const beforeResize=await d.boundingBox();assert(beforeResize);
  const resizeGrip=await d.locator(':scope > .surface-resize').boundingBox();assert(resizeGrip);
  await page.mouse.move(resizeGrip.x+resizeGrip.width-4,resizeGrip.y+resizeGrip.height-4);
  await page.mouse.down();
  await page.mouse.move(resizeGrip.x+resizeGrip.width+95,resizeGrip.y+resizeGrip.height+45,{steps:12});
  await page.mouse.up();
  if((await d.boundingBox()).width<=beforeResize.width+30)t.diagnostic(JSON.stringify({
    beforeResize,resizeGrip,after:await d.boundingBox(),
    handle:await d.locator(':scope > .surface-resize').evaluate(e=>({position:getComputedStyle(e).position,pointer:getComputedStyle(e).pointerEvents,parent:getComputedStyle(e.parentElement).position,css:e.parentElement.getAttribute('style')}))
  }));
  await page.waitForFunction(before=>document.querySelector('dialog.local-dictionary').getBoundingClientRect().width>before+30,beforeResize.width);
  assert.deepEqual(words,maskBeforeResize,'resizing cannot save or lose the active list');
  assert.deepEqual(allowWords,allowBeforeResize,'resizing cannot change the other list');
  await page.setViewportSize({width:420,height:760});
  const bounds=await d.boundingBox();assert(bounds&&bounds.x>=0&&bounds.x+bounds.width<=421);
  assert(await d.getByRole('button',{name:labels.save,exact:true}).isVisible(),'footer remains available after shrinking the viewport');
  if(process.env.MAGPIE_UI_ARTIFACTS){
    await fs.mkdir(process.env.MAGPIE_UI_ARTIFACTS,{recursive:true});
    await page.screenshot({path:path.join(process.env.MAGPIE_UI_ARTIFACTS,`dictionary-${engine}-${lang}-420.png`)});
  }
  await page.keyboard.press('Escape');await d.waitFor({state:'detached'});assert.deepEqual(errors,[]);
 });
}
}
