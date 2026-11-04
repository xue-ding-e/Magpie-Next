"""Linux CI-only synthetic proofs for nested ownership; no browser or Go."""
import importlib.util
import json
import os
from pathlib import Path
import re
import sys
import tempfile
import unittest

CONTROL=Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('outer_owned_proof',CONTROL/'owned_process.py')
owned=importlib.util.module_from_spec(spec);spec.loader.exec_module(owned)

# The grandchild blocks until main opens its pidfd. Main then releases it and
# waits for the stable pidfd exit event, not a guessed shutdown sleep. The
# wrapper may reap the zombie first; the opened pidfd still proves its exit.
DOUBLE_FORK='''import os,select
ready_r,ready_w=os.pipe()
go_r,go_w=os.pipe()
pid=os.fork()
if pid==0:
    os.close(ready_r);os.close(go_w)
    os.setsid()
    if os.fork()!=0:
        os.close(ready_w);os.close(go_r);os._exit(0)
    os.write(ready_w,(str(os.getpid())+"\\n").encode())
    os.close(ready_w)
    if os.read(go_r,1)!=b'x': os._exit(2)
    os.close(go_r);os._exit(0)
os.close(ready_w);os.close(go_r)
os.waitpid(pid,0)
ready=select.poll();ready.register(ready_r,select.POLLIN)
if not ready.poll(2000): raise RuntimeError('grandchild identity handshake timed out')
identity=os.read(ready_r,32);os.close(ready_r)
if not identity.endswith(b"\\n"): raise RuntimeError('incomplete identity handshake')
fd=os.pidfd_open(int(identity))
try:
    os.write(go_w,b'x');os.close(go_w)
    exited=select.poll();exited.register(fd,select.POLLIN)
    events=exited.poll(2000)
    if not events or not events[0][1]&select.POLLIN: raise RuntimeError('grandchild exit not proven')
finally:
    os.close(fd)
'''
# A real detached live descendant must outlive main and be handed back to the
# unchanged outer supervisor. The ready file proves it actually started.
LIVE='''import os,time
from pathlib import Path
pid=os.fork()
if pid==0:
    os.setsid()
    Path('ready').write_text('synthetic')
    time.sleep(30)
    os._exit(0)
end=time.monotonic()+2
while not Path('ready').exists():
    if time.monotonic()>end: raise RuntimeError('synthetic startup failed')
    time.sleep(.01)
'''

class Native(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if sys.platform!='linux': raise RuntimeError('native reaper proof requires Linux')
        owned.remaining_timeout(30)
        owned.linux_subreaper()  # Fail, never skip, if native ownership is absent.

    def probe(self,code,deny_reads=False):
        with tempfile.TemporaryDirectory(prefix='dictionary-inner-proof-',dir=os.environ['RUNNER_TEMP']) as directory:
            root=Path(directory)
            launch='''import importlib.util,sys
from pathlib import Path
spec=importlib.util.spec_from_file_location('inner_proof',sys.argv[1])
r=importlib.util.module_from_spec(spec);spec.loader.exec_module(r)
'''
            if deny_reads:
                launch+='''class DeniedPath:
    def __init__(self,*args): pass
    def read_text(self): raise OSError('synthetic proc read failure')
r.Path=DeniedPath
'''
            launch+='''try:
    result=r.run([sys.executable,'-I','-B','-c',sys.argv[2]],cwd='.')
except (Exception,r.owned.Cancelled):
    raise SystemExit(1)
raise SystemExit(result if result>=0 else 1)
'''
            result=owned.run_owned([sys.executable,'-I','-B','-c',launch,str(CONTROL/'browser_reaper.py'),code],cwd=root,environment=dict(os.environ),log_path=root/'proof.log',timeout=10)
            return result,(root/'proof.log').read_text(),(root/'ready').is_file()

    def test_normal_double_fork_zombies_are_actually_reaped(self):
        result,log,_=self.probe(DOUBLE_FORK)
        self.assertEqual(result,{'returncode':0,'timed_out':False,'orphaned':False,'clean':True})
        values=re.findall(r'^# browser reaper verified reaped=(\d+)$',log,re.M)
        self.assertEqual(len(values),1);self.assertGreaterEqual(int(values[0]),1)

    def test_live_setsid_is_not_swallowed(self):
        result,_,ready=self.probe(LIVE)
        self.assertTrue(ready)
        self.assertEqual(result,{'returncode':1,'timed_out':False,'orphaned':True,'clean':True})

    def test_nonzero_main_never_turns_green(self):
        result,_,_=self.probe('raise SystemExit(7)')
        self.assertEqual(result,{'returncode':7,'timed_out':False,'orphaned':False,'clean':True})

    def test_unknown_parent_read_fails_and_outer_cleans(self):
        result,_,ready=self.probe(LIVE,deny_reads=True)
        self.assertTrue(ready)
        self.assertEqual(result,{'returncode':1,'timed_out':False,'orphaned':True,'clean':True})

if __name__=='__main__':
    if sys.argv[1:]: raise SystemExit('fixed native proof only')
    unittest.main(verbosity=2,failfast=True)
