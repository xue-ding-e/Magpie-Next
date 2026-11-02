// Synthetic geometry experiment, NOT product acceptance; never alter original tests.
const {webkit}=require('playwright');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const css=fs.readFileSync(path.join(__dirname,'layout-minimal.css'),'utf8');
const html=`<div class="dialog"><div class="editor framed disconnect-ask"><div class="ehead">Synthetic disconnect</div><div class="ebody"><p>Synthetic explanation</p><div class="ag-diff-wrap"><div class="fixture-lines">Synthetic config changes</div></div><p class="ag-note">Synthetic retained-provider note</p></div><div class="bar">Synthetic cancel</div></div></div>`;
const scenarios=[
 ['fresh',[]],
 ['prior-overflow',['overflow-content','restore-content']],
 ['overflow-toggle',['hidden','auto']],
 ['direct-grid',['direct-grid']],
 ['zero-scrollbar',['zero-scrollbar']],
 ['stable-gutter',['stable-gutter']],
];
(async()=>{
 const browser=await webkit.launch();let rows=[];
 try{
  for(const [name,transitions] of scenarios){
   const context=await browser.newContext({viewport:{width:890,height:800},serviceWorkers:'block'});
   let denied=0;await context.route('**/*',async r=>{denied++;await r.abort();});await context.routeWebSocket('**/*',s=>{denied++;s.close()});
   const page=await context.newPage();page.setDefaultTimeout(3000);
   await page.setContent(`<style>${css}</style>${html}`);
   const frames=()=>page.evaluate(async()=>{const b=document.querySelector('.ebody'),d=document.querySelector('.ag-diff-wrap');const out=[];for(let i=0;i<12;i++){await new Promise(requestAnimationFrame);const s=getComputedStyle(b),r=b.getBoundingClientRect(),x=d.getBoundingClientRect();out.push({clientWidth:b.clientWidth,scrollWidth:b.scrollWidth,clientHeight:b.clientHeight,scrollHeight:b.scrollHeight,width:r.width,diffWidth:x.width,gapRight:r.right-parseFloat(s.paddingRight)-x.right,grid:s.gridTemplateColumns,overflow:s.overflowY,gutter:s.scrollbarGutter});}return out;});
   const samples=[{stage:'initial',frames:await frames()}];
   for(const transition of transitions){await page.evaluate(t=>{const b=document.querySelector('.ebody'),lines=document.querySelector('.fixture-lines');if(t==='overflow-content')lines.style.height='900px';if(t==='restore-content')lines.style.height='316px';if(t==='hidden')b.style.overflowY='hidden';if(t==='auto')b.style.overflowY='auto';if(t==='direct-grid')b.style.gridTemplateColumns='72px minmax(0, 1fr)';if(t==='zero-scrollbar')document.documentElement.classList.add('zero-scrollbar');if(t==='stable-gutter')b.style.scrollbarGutter='stable';},transition);samples.push({stage:transition,frames:await frames()});}
   assert.equal(denied,0,'minimal page must not request network');rows.push({name,samples,reproduced:samples.at(-1).frames.every(x=>Math.abs(x.gapRight-10)<1)});await context.close();
  }
 }finally{await browser.close();}
 const report=path.resolve(process.env.GITHUB_WORKSPACE,'diagnostic-reports/layout-minimal');fs.mkdirSync(report,{recursive:true});fs.writeFileSync(path.join(report,'result.json'),JSON.stringify({diagnosticOnly:true,scenarios:rows},null,2));console.log(JSON.stringify(rows.map(x=>({name:x.name,reproduced:x.reproduced}))));
})().catch(e=>{console.error(e);process.exitCode=1});
