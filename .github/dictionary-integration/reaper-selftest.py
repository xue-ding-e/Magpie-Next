"""Mock-only subreaper contract checks; never starts native processes."""
import importlib.util
import io
import ast
import json
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
        with self.assertRaisesRegex(r.ReaperError,'NODE_PID_REAPPEARED'):r.reap_adopted(1,final=True)
        opened.assert_not_called();waited.assert_not_called()
    def test_node_pid_reappearing_at_final_tail_fails(self):
        self.setup_case([stat()]*2,children=[[42],[1]])
        with self.assertRaisesRegex(r.ReaperError,'RESIDUAL_CHILDREN'):r.reap_adopted(1,final=True)
    def test_live_child_during_node_run_is_never_waited_or_killed(self):
        _,closed,waited=self.setup_case([stat('S')]*2)
        self.assertEqual(r.reap_adopted(1),0);waited.assert_not_called();closed.assert_called_once()
    def test_live_child_after_node_exit_fails(self):
        _,closed,waited=self.setup_case([stat('S')]*2)
        with self.assertRaisesRegex(r.ReaperError,'LIVE_CHILD_AT_EXIT'):r.reap_adopted(1,final=True)
        waited.assert_not_called();closed.assert_called_once()
    def test_unknown_parent_fails_without_wait(self):
        opened,_,waited=self.setup_case([stat(parent=999)])
        with self.assertRaisesRegex(r.ReaperError,'OWNERSHIP_UNVERIFIED'):r.reap_adopted(1,final=True)
        opened.assert_not_called();waited.assert_not_called()
    def test_recycled_identity_fails_without_wait(self):
        _,closed,waited=self.setup_case([stat(),stat(start='124')])
        with self.assertRaisesRegex(r.ReaperError,'IDENTITY_CHANGED'):r.reap_adopted(1,final=True)
        waited.assert_not_called();closed.assert_called_once()
    def test_unreadable_proc_is_not_empty_success(self):
        _,_,waited=self.setup_case([OSError('synthetic read failure')])
        with self.assertRaises(OSError):r.reap_adopted(1,final=True)
        waited.assert_not_called()
    def test_zero_wait_result_does_not_claim_reaping(self):
        _,closed,waited=self.setup_case([stat()]*2);waited.return_value=(0,0)
        with self.assertRaisesRegex(r.ReaperError,'ZOMBIE_NOT_REAPED'):r.reap_adopted(1,final=True)
        closed.assert_called_once()
    def test_nonzero_helper_exit_fails(self):
        self.setup_case([stat()]*2,status=7<<8)
        with self.assertRaisesRegex(r.ReaperError,'ABNORMAL_HELPER_EXIT'):r.reap_adopted(1,final=True)
    def test_signaled_helper_exit_fails(self):
        self.setup_case([stat()]*2,status=9)
        with self.assertRaisesRegex(r.ReaperError,'ABNORMAL_HELPER_EXIT'):r.reap_adopted(1,final=True)
    def test_residual_child_after_sweep_fails(self):
        self.setup_case([stat()]*2,children=[[42],[43]])
        with self.assertRaisesRegex(r.ReaperError,'RESIDUAL_CHILDREN'):r.reap_adopted(1,final=True)
    def test_over_cap_fails_without_dropping_children(self):
        opened,_,waited=self.setup_case([],children=[list(range(257))])
        with self.assertRaisesRegex(r.ReaperError,'CHILD_COUNT_LIMIT'):r.reap_adopted(1,final=True)
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
            with self.assertRaisesRegex(r.ReaperError,'BUDGET_EXHAUSTED'):r.run(['synthetic'],cwd='.')
            process.kill.assert_not_called();process.terminate.assert_not_called();process.wait.assert_not_called()

    def test_each_self_thrown_code_is_unique_and_known(self):
        tree=ast.parse(Path(r.__file__).read_text())
        codes=[]
        for n in ast.walk(tree):
            if isinstance(n,ast.Raise) and isinstance(n.exc,ast.Call) and isinstance(n.exc.func,ast.Name):
                self.assertNotEqual(n.exc.func.id,'RuntimeError')
                if n.exc.func.id=='ReaperError': codes.append(ast.literal_eval(n.exc.args[0]))
        self.assertEqual(len(codes),len(set(codes)))
        self.assertEqual(set(codes),set(r.ERROR_CODES))
    def test_fixed_failure_codes_never_print_exception_details(self):
        sentinel='PRIVATE_ARG_ENV_OR_PID_424242'
        out,err=io.StringIO(),io.StringIO()
        with mock.patch.object(r.sys,'stdout',out),mock.patch.object(r.sys,'stderr',err),mock.patch.dict(os.environ,{'PRIVATE_ENV':sentinel}):
            for code in sorted(r.ERROR_CODES):r.report_failure(r.ReaperError(code))
            r.report_failure(RuntimeError(sentinel))
            r.report_failure(OSError(sentinel))
            r.report_failure(r.ReaperError(sentinel))
            r.report_failure(r.owned.Cancelled(15))
        self.assertEqual(out.getvalue(),'');self.assertNotIn(sentinel,err.getvalue())
        for line in err.getvalue().splitlines():
            if line.startswith('BROWSER REAPER OPERATION '): continue
            self.assertTrue(line.startswith('BROWSER REAPER FAILED: '))
            self.assertIn(line.split(': ',1)[1],set(r.ERROR_CODES)|{'UNEXPECTED_ERROR','CANCELLED'})
    def test_abnormal_wait_status_reports_only_bounded_verified_metadata(self):
        for status,exitcode,signalno in ((7<<8,7,None),(15,None,15)):
            with self.subTest(status=status):
                out,err=io.StringIO(),io.StringIO()
                self.setup_case([stat()]*2,status=status)
                with mock.patch.object(r.sys,'stdout',out),mock.patch.object(r.sys,'stderr',err):
                    with self.assertRaisesRegex(r.ReaperError,'ABNORMAL_HELPER_EXIT'):r.reap_adopted(1,final=True)
                self.assertEqual(out.getvalue(),'')
                lines=err.getvalue().splitlines();self.assertEqual(len(lines),1)
                record=json.loads(lines[0].split(' ',3)[3])
                self.assertEqual(set(record),{'owned','comm','state','exitcode','signal'})
                self.assertEqual(record,{'owned':True,'comm':'synthetic','state':'Z','exitcode':exitcode,'signal':signalno})
    def test_diagnostic_write_failure_keeps_abnormal_failure_and_closes_pidfd(self):
        _,closed,waited=self.setup_case([stat()]*2,status=7<<8)
        err=mock.Mock();err.write.side_effect=OSError('PRIVATE_OUTPUT_DETAIL')
        with mock.patch.object(r.sys,'stderr',err):
            with self.assertRaisesRegex(r.ReaperError,'ABNORMAL_HELPER_EXIT'):r.reap_adopted(1,final=True)
        closed.assert_called_once_with(77);waited.assert_called_once_with(42,os.WNOHANG)
    def test_unverified_and_unreaped_cases_emit_no_process_metadata(self):
        for lines,result in (([stat(parent=999)],(42,0)),([stat(),stat()],(0,0))):
            with self.subTest(result=result):
                _,_,waited=self.setup_case(lines);waited.return_value=result
                err=io.StringIO()
                with mock.patch.object(r.sys,'stderr',err):
                    with self.assertRaises(r.ReaperError):r.reap_adopted(1,final=True)
                self.assertEqual(err.getvalue(),'')
    def test_comm_is_single_line_and_bounded(self):
        err=io.StringIO()
        with mock.patch.object(r.sys,'stderr',err):r.report_abnormal_helper('42 (bad\n'+('x'*100)+') Z',15)
        lines=err.getvalue().splitlines();self.assertEqual(len(lines),1)
        record=json.loads(lines[0].split(' ',3)[3]);self.assertLessEqual(len(record['comm']),32)

    def test_operation_stage_and_errno_are_fixed_and_private(self):
        sentinel='PRIVATE_PATH_ENV_PID_818181'
        for stage in r.STAGES:
            err,out=io.StringIO(),io.StringIO()
            problem=OSError(13,sentinel,sentinel)
            with mock.patch.object(r.sys,'stderr',err),mock.patch.object(r.sys,'stdout',out):
                try:r.observed(stage,mock.Mock(side_effect=problem))
                except OSError as error:r.report_failure(error)
            self.assertEqual(out.getvalue(),'');self.assertNotIn(sentinel,err.getvalue())
            record=json.loads(err.getvalue().splitlines()[1].split(' ',3)[3])
            self.assertEqual(record,{'stage':stage,'exception':'PermissionError','errno':13})
    def test_unknown_stage_class_and_errno_cannot_leak_strings(self):
        sentinel='PRIVATE_STAGE_CLASS_919191'
        Strange=type(sentinel,(OSError,),{})
        problem=Strange(999999999,sentinel)
        err=io.StringIO()
        with mock.patch.object(r.sys,'stderr',err):
            try:r.observed(sentinel,mock.Mock(side_effect=problem))
            except OSError as error:r.report_failure(error)
        self.assertNotIn(sentinel,err.getvalue())
        record=json.loads(err.getvalue().splitlines()[1].split(' ',3)[3])
        self.assertEqual(record,{'stage':'UNKNOWN','exception':'OtherError','errno':None})
    def test_successful_close_does_not_hide_failed_operation(self):
        problem=OSError(5,'PRIVATE_PROC_DETAIL');err=io.StringIO()
        with mock.patch.object(r.sys,'stderr',err):
            try:
                try:r.observed('STAT_AFTER',mock.Mock(side_effect=problem))
                finally:r.observed('PIDFD_CLOSE',lambda:None)
            except OSError as error:r.report_failure(error)
        self.assertIn('"stage": "STAT_AFTER"',err.getvalue());self.assertNotIn('PRIVATE_PROC_DETAIL',err.getvalue())
    def test_failed_close_reports_close_not_previous_operation(self):
        err=io.StringIO()
        with mock.patch.object(r.sys,'stderr',err):
            try:
                try:r.observed('WAITPID',mock.Mock(side_effect=OSError(5,'PRIVATE_WAIT')))
                finally:r.observed('PIDFD_CLOSE',mock.Mock(side_effect=OSError(9,'PRIVATE_CLOSE')))
            except OSError as error:r.report_failure(error)
        self.assertIn('"stage": "PIDFD_CLOSE"',err.getvalue());self.assertNotIn('PRIVATE_',err.getvalue())
    def test_different_error_does_not_inherit_stale_stage(self):
        err=io.StringIO()
        with mock.patch.object(r.sys,'stderr',err):
            try:r.observed('WAITPID',mock.Mock(side_effect=OSError('PRIVATE_STALE')))
            except OSError:pass
            r.report_failure(RuntimeError('PRIVATE_NEW'))
        self.assertIn('"stage": "UNKNOWN"',err.getvalue());self.assertNotIn('PRIVATE_',err.getvalue())
    def test_report_output_failure_never_escapes(self):
        for failure in (r.ReaperError('ABNORMAL_HELPER_EXIT'),RuntimeError('PRIVATE_ORIGINAL_DETAIL')):
            err=mock.Mock();err.write.side_effect=OSError('PRIVATE_WRITE_DETAIL')
            with mock.patch.object(r.sys,'stderr',err):
                self.assertIsNone(r.report_failure(failure))

    def test_running_zero_then_pid_counts_only_actual_reap(self):
        _,closed,waited=self.setup_case([stat()]*4,children=[[1,42],[1,42]])
        waited.side_effect=[(0,999999),(42,0)]
        err=io.StringIO()
        with mock.patch.object(r.sys,'stderr',err):
            self.assertEqual(r.reap_adopted(1),0)
            self.assertEqual(r.reap_adopted(1),1)
        self.assertEqual(waited.call_count,2);self.assertEqual(closed.call_count,2)
        self.assertEqual(err.getvalue(),'')
    def test_running_wrong_pid_still_fails(self):
        _,closed,waited=self.setup_case([stat()]*2);waited.return_value=(43,0)
        with self.assertRaisesRegex(r.ReaperError,'ZOMBIE_NOT_REAPED'):r.reap_adopted(1)
        closed.assert_called_once_with(77)
    def test_never_waitable_reaches_original_budget_failure(self):
        _,closed,waited=self.setup_case([stat()]*4,children=[[1,42],[1,42]])
        waited.return_value=(0,0)
        process=mock.Mock(pid=1);process.poll.return_value=None
        with mock.patch.object(r.owned,'remaining_timeout',return_value=1),mock.patch.object(r.owned,'linux_subreaper'),mock.patch.object(r.subprocess,'Popen',return_value=process),mock.patch.object(r.time,'monotonic',side_effect=[100,100.5,102]),mock.patch.object(r.time,'sleep') as sleep,mock.patch.object(r.sys,'stdout',io.StringIO()) as out:
            with self.assertRaisesRegex(r.ReaperError,'BUDGET_EXHAUSTED'):r.run(['synthetic'],cwd='.')
            self.assertEqual(out.getvalue(),'')
            self.assertEqual(waited.call_count,2);self.assertEqual(closed.call_count,2)
            sleep.assert_called_once_with(.02)
            process.kill.assert_not_called();process.terminate.assert_not_called();process.wait.assert_not_called()
    def test_pending_while_running_still_rejected_at_final(self):
        _,closed,waited=self.setup_case([stat()]*4,children=[[1,42],[42]])
        waited.return_value=(0,0)
        self.assertEqual(r.reap_adopted(1),0)
        with self.assertRaisesRegex(r.ReaperError,'ZOMBIE_NOT_REAPED'):r.reap_adopted(1,final=True)
        self.assertEqual(closed.call_count,2)

unittest.main()
