// Diagnostic-only, synthetic pages. No DOM writes, action options changes,
// request data, text content, headers or real profile access.
const fs = require('node:fs');
const path = require('node:path');
const pages = new WeakMap(), prototypes = new WeakSet(), contexts = new WeakMap();
let sequence = 0;
let diagnosticFailures = 0;
function markFailure() {
  diagnosticFailures++; process.exitCode=1;
  if(diagnosticFailures===1) { try { process.stderr.write('Diagnostic evidence unavailable; original test outcome retained.\n'); } catch {} }
}
process.once('exit',()=>{if(diagnosticFailures)process.exitCode=1;});
function safeAppend(file, value) {
  try { fs.appendFileSync(file, JSON.stringify(value)+'\n'); return true; }
  catch { markFailure(); return false; }
}
// A rejection, synchronous throw or hung diagnostic is recorded without
// replacing an original application/test error or creating an unhandled rejection.
function bestEffort(operation, milliseconds=1000) {
  return new Promise(resolve=>{
    let settled=false;
    const finish=result=>{if(!settled){settled=true;clearTimeout(timer);resolve(result);}};
    const timer=setTimeout(()=>{markFailure();finish({ok:false});},milliseconds);
    Promise.resolve().then(operation).then(value=>finish({ok:true,value}),()=>{markFailure();finish({ok:false});});
  });
}
function sample(element) {
  return new Promise(resolve => {
    const values = [];
    let done = false;
    const finish = () => { if (!done) { done = true; resolve(values); } };
    const timer = setTimeout(finish, 5500);
    function frame() {
      if (done) return;
      const r = element.getBoundingClientRect(), c = getComputedStyle(element);
      const at = document.elementFromPoint(r.left+r.width/2,r.top+r.height/2);
      const ancestors = [];
      for (let p=element;p && ancestors.length<8;p=p.parentElement) {
        const a=getComputedStyle(p);
        ancestors.push({tag:p.tagName,hidden:p.hidden,inert:p.inert,display:a.display,visibility:a.visibility,scrollTop:p.scrollTop,scrollLeft:p.scrollLeft});
      }
      values.push({time:performance.now(),connected:element.isConnected,rect:{x:r.x,y:r.y,width:r.width,height:r.height},disabled:element.disabled,ariaDisabled:element.getAttribute('aria-disabled'),display:c.display,visibility:c.visibility,opacity:c.opacity,transform:c.transform,translate:c.translate,pointerEvents:c.pointerEvents,hitTarget:at===element||element.contains(at),hitTag:at?.tagName,animations:element.getAnimations().map(a=>({playState:a.playState,currentTime:a.currentTime})),ancestors});
      if(values.length===90 || !element.isConnected) {clearTimeout(timer);finish();} else requestAnimationFrame(frame);
    }
    requestAnimationFrame(frame);
  });
}
function attachPage(page, directory) {
  if (!directory || pages.has(page)) return;
  try { fs.mkdirSync(directory,{recursive:true}); } catch { markFailure(); return; }
  pages.set(page,{directory});
  const proto=Object.getPrototypeOf(page.locator('body'));
  if (prototypes.has(proto)) return;
  prototypes.add(proto);
  const click=proto.click;
  proto.click=async function (...args) {
    const state=pages.get(this.page()), description=String(this);
    if (!state || !description.includes('.lib-ag.all') || !description.includes('docs')) return click.apply(this,args);
    const id=++sequence;
    // Run the read-only sampler concurrently. Never wait before invoking the
    // original action, delay success, scroll, focus, force, or change timeout.
    bestEffort(()=>this.evaluate(sample),5500).then(result=>safeAppend(path.join(state.directory,'click-samples.jsonl'),result.ok ? {id,values:result.value}:{id,samplingEnded:true})).catch(markFailure);
    try { return await click.apply(this,args); }
    catch (error) {
      try {
        const result=await bestEffort(()=>this.page().screenshot({path:path.join(state.directory,`failed-click-${id}.png`),timeout:1000}),1500);
        if(!result.ok)safeAppend(path.join(state.directory,'click-samples.jsonl'),{id,screenshotUnavailable:true});
      } finally { throw error; } // exact original Error, even when diagnostics fail
    }
  };
}
async function startTrace(context,directory) {
  if (!directory || contexts.has(context)) return;
  try { fs.mkdirSync(directory,{recursive:true}); } catch { markFailure(); return; }
  const file=path.join(directory,`context-${++sequence}.zip`);
  const started=await bestEffort(()=>context.tracing.start({screenshots:true,snapshots:true,sources:false}),2000);
  if(!started.ok)return;
  let active=true;
  const stop=async()=>{if(active){active=false;await bestEffort(()=>context.tracing.stop({path:file}),2000);}};
  contexts.set(context,stop);
  const close=context.close.bind(context);
  context.close=async(...args)=>{let failure;try{await stop();}catch(e){failure=e;}const value=await close(...args);if(failure)throw failure;return value;};
}
async function finishTraces(browser) {
  for(const context of browser.contexts()) {const stop=contexts.get(context);if(stop)await stop();}
}
module.exports={attachPage,startTrace,finishTraces,safeAppend,bestEffort,markFailure};
