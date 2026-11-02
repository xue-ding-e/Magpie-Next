"""Small synthetic child lifecycle checks; no browser, Go, cgroup or ACL runs."""
import importlib.util
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

class MockLifecycle(unittest.TestCase):
    def setUp(self):
        start=time.monotonic()
        self.budget=mock.patch.dict(os.environ,{'DICTIONARY_JOB_START_MONOTONIC':f'{start:.6f}','DICTIONARY_JOB_DEADLINE_MONOTONIC':f'{start+720:.6f}','DICTIONARY_JOB_CAP_SECONDS':'900'})
        self.budget.start()
        self.addCleanup(self.budget.stop)

    def test_no_launch_when_ownership_cannot_be_proved(self):
        with tempfile.TemporaryDirectory(prefix='dictionary-control-') as directory, mock.patch.object(owned,'linux_subreaper',side_effect=RuntimeError('synthetic missing ownership')), mock.patch.object(owned.subprocess,'Popen') as launch:
            with self.assertRaisesRegex(RuntimeError,'missing ownership'):
                owned.run_owned(['synthetic'],cwd=directory,environment={},log_path=Path(directory)/'unused.log',timeout=1)
            launch.assert_not_called()

    def test_all_exception_paths_cleanup_before_return(self):
        for failure in (None,RuntimeError('synthetic wait failure'),owned.Cancelled(signal.SIGTERM),KeyboardInterrupt()):
            with self.subTest(failure=type(failure).__name__),tempfile.TemporaryDirectory(prefix='dictionary-control-') as directory:
                process=mock.Mock()
                process.wait.side_effect=[failure,0] if failure is not None else [0,0]
                with mock.patch.object(owned,'linux_subreaper'),mock.patch.object(owned,'linux_reap_owned',return_value=False) as cleanup,mock.patch.object(owned.subprocess,'Popen',return_value=process):
                    if failure is not None:
                        with self.assertRaises(type(failure)):
                            owned.run_owned(['synthetic'],cwd=directory,environment={},log_path=Path(directory)/'child.log',timeout=1)
                    else:
                        self.assertTrue(owned.run_owned(['synthetic'],cwd=directory,environment={},log_path=Path(directory)/'child.log',timeout=1)['clean'])
                    cleanup.assert_called_once()

    def test_reaper_exception_still_stops_direct_child(self):
        with tempfile.TemporaryDirectory(prefix='dictionary-control-') as directory:
            process=mock.Mock()
            process.wait.side_effect=[0,0]
            process.poll.return_value=None
            with mock.patch.object(owned,'linux_subreaper'),mock.patch.object(owned,'linux_reap_owned',side_effect=RuntimeError('synthetic reaper failure')),mock.patch.object(owned.subprocess,'Popen',return_value=process):
                with self.assertRaisesRegex(RuntimeError,'synthetic reaper failure'):
                    owned.run_owned(['synthetic'],cwd=directory,environment={},log_path=Path(directory)/'child.log',timeout=1)
                process.kill.assert_called_once()
                self.assertEqual(process.wait.call_count,2)

    def test_unbound_windows_gate_is_stopped_if_query_also_fails(self):
        from pathlib import PosixPath
        with tempfile.TemporaryDirectory(prefix='dictionary-control-') as directory:
            process=mock.Mock()
            process.poll.return_value=None
            job=mock.Mock()
            job.assign.side_effect=RuntimeError('synthetic assign failure')
            job.reap.side_effect=RuntimeError('synthetic query failure')
            with mock.patch.object(owned.os,'name','nt'),mock.patch.object(owned,'Path',PosixPath),mock.patch.object(owned,'WindowsJob',return_value=job),mock.patch.object(owned.subprocess,'Popen',return_value=process):
                with self.assertRaisesRegex(RuntimeError,'synthetic query failure'):
                    owned.run_owned(['synthetic'],cwd=directory,environment={},log_path=PosixPath(directory)/'child.log',timeout=1)
                process.stdin.write.assert_not_called()
                process.kill.assert_called_once()
                process.wait.assert_called_once()
                job.close.assert_called_once()

    def test_resource_cancel_cleans_only_owned_group(self):
        resources=module('dictionary_resource_selftest','resources.py')
        group=mock.Mock()
        process=mock.Mock(pid=314159)
        process.wait.side_effect=[resources.owned.Cancelled(signal.SIGTERM),0]
        process.poll.return_value=None
        with mock.patch.object(resources,'create_group',return_value=group),mock.patch.object(resources,'cleanup_group') as cleanup,mock.patch.object(resources.subprocess,'Popen',return_value=process),mock.patch.object(resources.os,'killpg') as kill:
            with self.assertRaises(resources.owned.Cancelled):
                with resources.guarded_group('synthetic-parent') as owned_group:
                    resources.execute(owned_group,['--probe','synthetic'],1)
            kill.assert_called_once_with(314159,signal.SIGKILL)
            cleanup.assert_called_once_with(group)

class BudgetGate(unittest.TestCase):
    def test_missing_invalid_or_expired_deadline_never_spawns(self):
        base={'DICTIONARY_JOB_START_MONOTONIC':'1000','DICTIONARY_JOB_DEADLINE_MONOTONIC':'1720','DICTIONARY_JOB_CAP_SECONDS':'900'}
        variants=[{key:'' for key in base},dict(base,DICTIONARY_JOB_START_MONOTONIC='NaN'),dict(base,DICTIONARY_JOB_CAP_SECONDS='1200'),dict(base,DICTIONARY_JOB_START_MONOTONIC='2000',DICTIONARY_JOB_DEADLINE_MONOTONIC='2720'),dict(base,DICTIONARY_JOB_DEADLINE_MONOTONIC='1721'),dict(base,DICTIONARY_JOB_START_MONOTONIC='300',DICTIONARY_JOB_DEADLINE_MONOTONIC='1020')]
        for env in variants:
            with self.subTest(env=env),tempfile.TemporaryDirectory(prefix='dictionary-budget-') as directory,mock.patch.dict(os.environ,env),mock.patch.object(owned.time,'monotonic',return_value=1000),mock.patch.object(owned.subprocess,'Popen') as launch:
                with self.assertRaises(RuntimeError):
                    owned.run_owned(['synthetic'],cwd=directory,environment={},log_path=Path(directory)/'unused.log',timeout=300)
                launch.assert_not_called()

    def test_remaining_job_budget_shortens_actual_wait(self):
        env={'DICTIONARY_JOB_START_MONOTONIC':'330','DICTIONARY_JOB_DEADLINE_MONOTONIC':'1050','DICTIONARY_JOB_CAP_SECONDS':'900'}
        process=mock.Mock()
        process.wait.side_effect=[0,0]
        process.poll.return_value=0
        with tempfile.TemporaryDirectory(prefix='dictionary-budget-') as directory,mock.patch.dict(os.environ,env),mock.patch.object(owned.time,'monotonic',return_value=1000),mock.patch.object(owned,'linux_subreaper'),mock.patch.object(owned,'linux_reap_owned',return_value=False),mock.patch.object(owned.subprocess,'Popen',return_value=process):
            result=owned.run_owned(['synthetic'],cwd=directory,environment={},log_path=Path(directory)/'child.log',timeout=300)
            self.assertTrue(result['clean'])
            self.assertEqual(process.wait.call_args_list[0],mock.call(timeout=20.0))

if __name__=='__main__':
    require_native='--require-native' in sys.argv
    if require_native:
        cases=[Lifecycle] if sys.platform=='linux' else []
        if not cases:
            raise SystemExit('ENVIRONMENT NOT RUN: no supported native ownership control')
    else:
        if sys.platform!='linux':
            raise SystemExit('backend-only validation requires Linux ownership controls')
        cases=[BudgetGate,MockLifecycle,Lifecycle]
    suite=unittest.TestSuite(unittest.defaultTestLoader.loadTestsFromTestCase(case) for case in cases)
    result=unittest.TextTestRunner(verbosity=2,failfast=require_native).run(suite)
    if require_native and result.skipped:
        print('ENVIRONMENT NOT RUN: native lifecycle assurance is required; skipped fixtures never enable heavy tests',file=sys.stderr)
    raise SystemExit(0 if result.wasSuccessful() and not (require_native and result.skipped) else 1)
