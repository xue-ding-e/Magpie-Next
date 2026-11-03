"""Generate only a synthetic diagnostic copy; keep original assertions unchanged."""
import hashlib,pathlib,sys
EXPECTED='3e7a452fa6a0400cdfd2d6b9f3910e80028f1203f403fb7db28af497aa529ce6'
p=pathlib.Path(sys.argv[1]);out=pathlib.Path(sys.argv[2]);s=p.read_text();assert hashlib.sha256(p.read_bytes()).hexdigest()==EXPECTED
old='''          text: n.textContent,
          cut: n.scrollWidth > n.clientWidth || [...n.children].some((s) => s.scrollWidth > s.clientWidth),'''
new='''          text: n.textContent,
          cut: n.scrollWidth > n.clientWidth || [...n.children].some((s) => s.scrollWidth > s.clientWidth),
          diagnostic: (() => {
            const box = e => {
              const r = e.getBoundingClientRect(), c = getComputedStyle(e);
              return {tag:e.tagName,classes:e.className,rect:{x:r.x,y:r.y,width:r.width,height:r.height},
                clientWidth:e.clientWidth,scrollWidth:e.scrollWidth,offsetWidth:e.offsetWidth,
                clientHeight:e.clientHeight,scrollHeight:e.scrollHeight,offsetHeight:e.offsetHeight,
                display:c.display,overflowX:c.overflowX,whiteSpace:c.whiteSpace,flexBasis:c.flexBasis,
                flexShrink:c.flexShrink,flexWrap:c.flexWrap,minWidth:c.minWidth,maxWidth:c.maxWidth,columnGap:c.columnGap,font:c.font,padding:c.padding,border:c.border};
            };
            return {self:box(n),parts:[...n.children].map(box),labels:box(n.parentElement),
              quota:box(n.parentElement.parentElement),windows:n.closest('.quota-windows') ? box(n.closest('.quota-windows')) : null,
              stacked:!!n.closest('.quota-windows.stacked'),parentClasses:n.parentElement.className};
          })(),'''
assert s.count(old)==1;s=s.replace(old,new)
old='''        assert.equal(counts.length, 3);'''
new='''        if (counts.some(c => c.cut)) {
          console.log('QUOTA_CUT_DIAGNOSTIC ' + JSON.stringify({engine:name,width,counts}));
          const dir = process.env.AUTH_FULL_REPORTS;
          let captureTimer;
          try {
            if (!dir) throw new Error('Missing synthetic diagnostic report directory');
            await Promise.race([
              page.screenshot({path:path.join(dir, `quota-cut-${name}-${width}.png`),timeout:1000}),
              new Promise((_, reject) => { captureTimer = setTimeout(() => reject(new Error('capture deadline')), 1500); }),
            ]);
            console.log('QUOTA_DIAGNOSTIC_CAPTURE_OK ' + JSON.stringify({engine:name,width,synthetic:true}));
          } catch (error) {
            console.log('QUOTA_DIAGNOSTIC_CAPTURE_FAILED ' + JSON.stringify({engine:name,width,reason:String(error),synthetic:true}));
            process.exitCode = 1;
          } finally { clearTimeout(captureTimer); }
        }
        assert.equal(counts.length, 3);'''
assert s.count(old)==1;s=s.replace(old,new)
# All original assertions and their messages remain intact. A screenshot
# error is separately marked; it never replaces the original assertion.
out.parent.mkdir(parents=True,exist_ok=True);out.write_text(s)
print(hashlib.sha256(out.read_bytes()).hexdigest())
