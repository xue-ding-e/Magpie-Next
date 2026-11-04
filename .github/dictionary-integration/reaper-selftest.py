"""Mock-only subreaper contract checks; never starts native processes."""
import importlib.util
import io
import os
from pathlib import Path
import unittest
from unittest import mock
spec=importlib.util.spec_from_file_location('reaper',Path(__file__).with_name('browser_reaper.py'))
r=importlib.util.module_from_spec(spec);spec.loader.exec_module(r)
def stat(state='Z',parent=100,start='123'):
    return '42 (synthetic) '+' '.join([state,str(parent)]+['0']*17+[start])

class Reaper(unittest.TestCase):
    def setup_case(self,states,children=None,status=0):
        stack=__import__('contextlib').ExitStack();self.addCleanup(stack.close)
        stack.enter_context(mock.patch.object(r.owned,'linux_children',side_effect=children or [[42],[]]))
        stack.enter_context(mock.patch.object(r.os,'getpid',return_value=100))
        stack.enter_context(mock.patch.object(r.Path,'read_text',side_effect=states))
        opened=stack.enter_context(mock.patch.object(r.os,'pidfd_open',return_value=77))
        closed=stack.enter_context(mock.patch.object(r.os,'close'))
        waited=stack.enter_context(mock.patch.object(r.os,'waitpid',return_value=(42,status)))
        return opened,closed,waited
    def test_zombie_is_actually_reaped_and_node_is_excluded(self):
        opened,closed,waited=self.setup_case([stat(),stat()])
        self.assertEqual(r.reap_adopted(1,final=True),1)
        opened.assert_called_once_with(42);waited.assert_called_once_with(42,os.WNOHANG);closed.assert_called_once_with(77)
    def test_running_node_pid_is_excluded(self):
        opened,_,waited=self.setup_case([],children=[[1]])
        self.assertEqual(r.reap_adopted(1),0)
        opened.assert_not_called();waited.assert_not_called()
    def test_node_pid_reappearing_after_poll_fails_without_wait(self):
        opened,_,waited=self.setup_case([],children=[[1]])
        with self.assertRaisesRegex(RuntimeError,'node PID reappeared'):r.reap_adopted(1,final=True)
        opened.assert_not_called();waited.assert_not_called()
    def test_node_pid_reappearing_at_final_tail_fails(self):
        self.setup_case([stat()]*2,children=[[42],[1]])
        with self.assertRaisesRegex(RuntimeError,'remain'):r.reap_adopted(1,final=True)
    def test_live_child_during_node_run_is_never_waited_or_killed(self):
        _,closed,waited=self.setup_case([stat('S')]*2)
        self.assertEqual(r.reap_adopted(1),0);waited.assert_not_called();closed.assert_called_once()
    def test_live_child_after_node_exit_fails(self):
        _,closed,waited=self.setup_case([stat('S')]*2)
        with self.assertRaisesRegex(RuntimeError,'live adopted'):r.reap_adopted(1,final=True)
        waited.assert_not_called();closed.assert_called_once()
    def test_unknown_parent_fails_without_wait(self):
        opened,_,waited=self.setup_case([stat(parent=999)])
        with self.assertRaises(RuntimeError):r.reap_adopted(1,final=True)
        opened.assert_not_called();waited.assert_not_called()
    def test_recycled_identity_fails_without_wait(self):
        _,closed,waited=self.setup_case([stat(),stat(start='124')])
        with self.assertRaises(RuntimeError):r.reap_adopted(1,final=True)
        waited.assert_not_called();closed.assert_called_once()
    def test_unreadable_proc_is_not_empty_success(self):
        _,_,waited=self.setup_case([OSError('synthetic read failure')])
        with self.assertRaises(OSError):r.reap_adopted(1,final=True)
        waited.assert_not_called()
    def test_zero_wait_result_does_not_claim_reaping(self):
        _,closed,waited=self.setup_case([stat()]*2);waited.return_value=(0,0)
        with self.assertRaisesRegex(RuntimeError,'not reaped'):r.reap_adopted(1,final=True)
        closed.assert_called_once()
    def test_nonzero_helper_exit_fails(self):
        self.setup_case([stat()]*2,status=7<<8)
        with self.assertRaisesRegex(RuntimeError,'not exit normally'):r.reap_adopted(1,final=True)
    def test_signaled_helper_exit_fails(self):
        self.setup_case([stat()]*2,status=9)
        with self.assertRaisesRegex(RuntimeError,'not exit normally'):r.reap_adopted(1,final=True)
    def test_residual_child_after_sweep_fails(self):
        self.setup_case([stat()]*2,children=[[42],[43]])
        with self.assertRaisesRegex(RuntimeError,'remain'):r.reap_adopted(1,final=True)
    def test_over_cap_fails_without_dropping_children(self):
        opened,_,waited=self.setup_case([],children=[list(range(257))])
        with self.assertRaisesRegex(RuntimeError,'too many'):r.reap_adopted(1,final=True)
        opened.assert_not_called();waited.assert_not_called()
    def test_node_nonzero_status_preserved(self):
        process=mock.Mock(pid=1);process.poll.return_value=7
        with mock.patch.object(r.owned,'remaining_timeout',return_value=10),mock.patch.object(r.owned,'linux_subreaper'),mock.patch.object(r.subprocess,'Popen',return_value=process),mock.patch.object(r,'reap_adopted',return_value=2) as sweep,mock.patch.object(r.sys,'stdout',io.StringIO()):
            self.assertEqual(r.run(['synthetic'],cwd='.'),7);sweep.assert_called_once_with(1,final=True)
            process.kill.assert_not_called();process.terminate.assert_not_called();process.wait.assert_not_called()
    def test_missing_budget_never_launches(self):
        with mock.patch.object(r.owned,'remaining_timeout',side_effect=RuntimeError('budget')),mock.patch.object(r.subprocess,'Popen') as launch:
            with self.assertRaises(RuntimeError):r.run(['synthetic'],cwd='.')
            launch.assert_not_called()
    def test_budget_expiry_does_not_kill_or_extend(self):
        process=mock.Mock(pid=1);process.poll.return_value=None
        with mock.patch.object(r.owned,'remaining_timeout',return_value=1),mock.patch.object(r.owned,'linux_subreaper'),mock.patch.object(r.subprocess,'Popen',return_value=process),mock.patch.object(r,'reap_adopted',return_value=0),mock.patch.object(r.time,'monotonic',side_effect=[100,102]):
            with self.assertRaisesRegex(RuntimeError,'budget exhausted'):r.run(['synthetic'],cwd='.')
            process.kill.assert_not_called();process.terminate.assert_not_called();process.wait.assert_not_called()

unittest.main()
