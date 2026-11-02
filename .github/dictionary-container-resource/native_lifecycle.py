"""Small synthetic child lifecycle checks; no browser, Go, cgroup or ACL runs."""
import importlib.util
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
import unittest
from unittest import mock

CONTROL=Path(__file__).resolve().parent

def module(name,file):
    spec=importlib.util.spec_from_file_location(name,CONTROL/file)
    value=importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value

owned=module('dictionary_owned_selftest','owned_process.py')

class Lifecycle(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        try:
            owned.linux_subreaper()
        except (OSError,RuntimeError) as error:
            raise unittest.SkipTest('ENVIRONMENT NOT RUN: five native Linux lifecycle scenarios: ' + str(error))

    def run_child(self, code, timeout=5):
        with tempfile.TemporaryDirectory(prefix='dictionary-lifecycle-') as directory:
            return owned.run_owned([sys.executable,'-I','-B','-c',code],cwd=directory,
                environment=dict(os.environ),log_path=Path(directory)/'child.log',timeout=timeout)

    def test_normal_exit_has_no_children(self):
        result=self.run_child("print('synthetic child')")
        self.assertEqual(result,{'returncode':0,'timed_out':False,'orphaned':False,'clean':True})

    def test_detached_descendant_is_owned_and_reaped(self):
        result=self.run_child("import subprocess,sys; subprocess.Popen([sys.executable,'-I','-B','-c','import time; time.sleep(30)'],start_new_session=True)")
        self.assertTrue(result['orphaned']);self.assertTrue(result['clean'])
        self.assertEqual(owned.linux_children(),[])

    def test_timeout_reaps_detached_tree(self):
        result=self.run_child("import subprocess,sys,time; subprocess.Popen([sys.executable,'-I','-B','-c','import time; time.sleep(30)'],start_new_session=True); time.sleep(30)",timeout=.2)
        self.assertTrue(result['timed_out']);self.assertTrue(result['clean'])
        self.assertEqual(owned.linux_children(),[])

    def test_sigterm_enters_cleanup(self):
        with self.assertRaises(owned.Cancelled):
            self.run_child("import os,signal,time; os.kill(os.getppid(),signal.SIGTERM); time.sleep(30)")
        self.assertEqual(owned.linux_children(),[])

    def test_wait_exception_enters_cleanup(self):
        original=subprocess.Popen.wait
        first=True
        def broken(process,*args,**kwargs):
            nonlocal first
            if first:
                first=False
                raise RuntimeError('synthetic wait failure')
            return original(process,*args,**kwargs)
        with mock.patch.object(subprocess.Popen,'wait',broken):
            with self.assertRaisesRegex(RuntimeError,'synthetic wait failure'):
                self.run_child('import time; time.sleep(30)')
        self.assertEqual(owned.linux_children(),[])

if __name__ == '__main__':
    suite = unittest.defaultTestLoader.loadTestsFromTestCase(Lifecycle)
    result = unittest.TextTestRunner(verbosity=2, failfast=True).run(suite)
    clean = not owned.linux_children() if not result.skipped else False
    passed = result.wasSuccessful() and result.testsRun == 5 and not result.skipped and clean
    report = {'ok': passed, 'tests_run': result.testsRun, 'failures': len(result.failures),
              'errors': len(result.errors), 'skips': len(result.skipped), 'owned_children_empty': clean}
    with (Path(os.environ['DICTIONARY_REPORTS']) / 'native-cases.json').open('x') as stream:
        json.dump(report, stream)
    raise SystemExit(0 if passed else 1)
