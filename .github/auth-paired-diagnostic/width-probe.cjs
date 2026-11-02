// Diagnostic-only style trials on the full synthetic failing fixture.
// Preserve the original layout evaluation result, so its original assertion
// still fails even if a trial invalidates WebKit's cached layout.
const d=require('./readonly-diagnostics.cjs');
const patched=new WeakSet(),pages=new WeakMap();
function trials(editor){
 const body=editor.querySelector('.ebody'),diff=editor.querySelector('.ag-diff-wrap');
 if(!body||!diff)throw Error('Synthetic width fixture missing');
 const ancestors=[body,editor,editor.closest('.dialog'),editor.closest('.modal')].filter(Boolean);
 const originals=ancestors.map(e=>[e,e.getAttribute('style'),e.scrollTop,e.scrollLeft]);
 const rect=e=>{const r=e.getBoundingClientRect();return {width:r.width,height:r.height,left:r.left,right:r.right,top:r.top,bottom:r.bottom}};
 const box=e=>{const s=getComputedStyle(e);return {tag:e.tagName,className:e.className,rect:rect(e),clientWidth:e.clientWidth,scrollWidth:e.scrollWidth,offsetWidth:e.offsetWidth,clientHeight:e.clientHeight,scrollHeight:e.scrollHeight,offsetHeight:e.offsetHeight,scrollTop:e.scrollTop,scrollLeft:e.scrollLeft,actualVerticalOverflow:e.scrollHeight>e.clientHeight,actualHorizontalOverflow:e.scrollWidth>e.clientWidth,css:Object.fromEntries(['display','position','boxSizing','width','height','minHeight','maxHeight','flex','gridTemplateColumns','gridTemplateRows','gap','paddingLeft','paddingRight','overflowX','overflowY','scrollbarGutter','scrollbarWidth'].map(k=>[k,s[k]])),scrollbar:getComputedStyle(e,'::-webkit-scrollbar').width}};
 const measure=()=>{const b=box(body),r=rect(diff);return {ancestors:ancestors.map(box),diff:r,gapRight:b.rect.right-parseFloat(b.css.paddingRight)-r.right}};
 const restore=()=>{for(const [e,s,top,left] of originals){if(s===null)e.removeAttribute('style');else e.setAttribute('style',s);e.scrollTop=top;e.scrollLeft=left}};
 const mutations=[['read-control',()=>{}],['body-overflow-hidden',()=>{body.style.overflowY='hidden'}],['body-direct-grid',()=>{body.style.gridTemplateColumns='72px minmax(0, 1fr)'}],['body-scrollbar-none',()=>{body.style.scrollbarWidth='none'}],['body-force-real-overflow',()=>{body.style.maxHeight='200px'}],['editor-grid',()=>{editor.style.display='grid'}]];
 const values=[];const initial=measure();
 try{
  for(const [name,change] of mutations){
   const before=measure();
   try{change();values.push({name,before,during:measure(),eligibleTenPixelBaseline:Math.abs(before.gapRight-10)<1});}
   finally{restore();}
   values.at(-1).restored=measure();
   values.at(-1).styleRestored=originals.every(([e,s])=>e.getAttribute('style')===s);
  }
  return {initial,values,final:measure(),note:'Trials may invalidate cached layout; later trials without the same10px precondition cannot establish causality. Original fixture assertion receives its pre-trial measurement.'};
 }finally{restore();}
}
function attach(page,file){
 if(!file)return;
 pages.set(page,{file,observed:false});const proto=Object.getPrototypeOf(page.locator('body'));
 if(patched.has(proto))return;patched.add(proto);const evaluate=proto.evaluate;
 proto.evaluate=async function(...args){
  const result=await evaluate.apply(this,args); // original Error and arguments untouched
  const state=pages.get(this.page());
  if(!state||state.observed||!String(this).includes('.editor.disconnect-ask')||!result?.content||!result?.diff||!Number.isFinite(result.content.right)||!Number.isFinite(result.diff.right)||Math.abs(result.content.right-result.diff.right-10)>=1)return result;
  state.observed=true;
  const diagnostic=await d.bestEffort(()=>evaluate.call(this,trials),1500);
  d.safeAppend(state.file,diagnostic.ok?diagnostic.value:{unavailable:true});
  return result; // exact original object: no adjusted geometry/tolerance
 };
}
module.exports={attach,trials};
