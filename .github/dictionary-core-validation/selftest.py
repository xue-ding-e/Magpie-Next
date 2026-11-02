"""Lightweight local checks only: never starts Go, browsers, Windows or stress."""
import ast
import contextlib
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import unittest
from unittest import mock

CONTROL=Path(__file__).resolve().parent
ROOT=CONTROL.parents[1]

class Controls(unittest.TestCase):
    def test_active_source_is_explicit(self):
        m=json.loads((CONTROL/'source.json').read_text())
        self.assertTrue(m['enabled'])
        self.assertEqual(m['source'],'203a1811105a97fa46cb62fcf9706f44535b0ebd')
        self.assertEqual(m['source_tree'],'0ceb41a3c81cf0b562f902fc8ad688733cdbf393')
        self.assertEqual(len(m['files']),17)
        result=subprocess.run([sys.executable,'-I','-B',str(CONTROL/'gate.py')],capture_output=True,text=True,env={})
        self.assertNotEqual(result.returncode,0)
        self.assertIn('wrong repository',result.stderr)

    def gate_result(self, *, environment=None, answers=None, enabled=True):
        # Pure identity-gate mocks: no checkout, Git object write, remote call,
        # Go process or native lifecycle workload. Real control bytes stay hashed.
        spec=importlib.util.spec_from_file_location('dictionary_gate_activation',CONTROL/'gate.py')
        gate=importlib.util.module_from_spec(spec)
        spec.loader.exec_module(gate)
        m=json.loads((CONTROL/'source.json').read_text())
        m['enabled']=enabled
        product={ROOT/name:('synthetic:'+name).encode() for name in m['files']}
        m['files']={name:hashlib.sha256(product[ROOT/name]).hexdigest() for name in m['files']}
        hashes=json.loads((CONTROL/'control-hashes.json').read_text())
        expected=set(hashes)|{'.github/dictionary-core-validation/control-hashes.json'}
        head='1'*40
        replies={
            ('rev-parse','HEAD'):head,
            ('rev-list','--parents','-n','1',head):head+' '+m['source'],
            ('rev-list','--parents','-n','1',m['source']):m['source']+' '+m['base'],
            ('rev-parse',m['source']+'^{tree}'):m['source_tree'],
            ('diff','--name-only',m['base'],m['source']):'\n'.join(sorted(m['files'])),
            ('diff','--name-only',m['source'],head):'\n'.join(sorted(expected)),
            ('status','--porcelain','--untracked-files=all'):'',
        }
        for name in hashes:
            replies[('ls-tree',head,'--',name)]='100644 blob '+'2'*40+'\t'+name
        replies.update(answers or {})
        env={'GITHUB_REPOSITORY':m['repository'],'GITHUB_REF':m['branch'],
             'GITHUB_EVENT_NAME':'push','GITHUB_SHA':head}
        env.update(environment or {})
        original_text=Path.read_text
        original_bytes=Path.read_bytes
        def read_text(path,*args,**kwargs):
            if path==CONTROL/'source.json':
                return json.dumps(m)
            return original_text(path,*args,**kwargs)
        def read_bytes(path):
            return product[path] if path in product else original_bytes(path)
        output=io.StringIO()
        with mock.patch.dict(os.environ,env,clear=True), mock.patch.object(gate,'git',side_effect=lambda *args:replies[args]), mock.patch.object(Path,'read_text',read_text), mock.patch.object(Path,'read_bytes',read_bytes), contextlib.redirect_stdout(output):
            try:
                gate.main()
            except SystemExit as error:
                return str(error)
        self.assertIn('PASS reviewed backend tree',output.getvalue())
        return None

    def test_gate_accepts_only_reviewed_context(self):
        self.assertIsNone(self.gate_result())
        self.assertIsNone(self.gate_result(environment={'GITHUB_EVENT_NAME':'workflow_dispatch'}))
        self.assertIn('NOT ACTIVATED',self.gate_result(enabled=False))

    def test_gate_rejects_wrong_repository_ref_event_and_checkout(self):
        for key,value,error in (
            ('GITHUB_REPOSITORY','yetone/magpie','wrong repository'),
            ('GITHUB_REF','refs/heads/main','wrong candidate branch'),
            ('GITHUB_REF','refs/tags/v0.0.0','wrong candidate branch'),
            ('GITHUB_REF','refs/heads/validation/local-dictionary-20261102','wrong candidate branch'),
            ('GITHUB_EVENT_NAME','pull_request_target','wrong event'),
            ('GITHUB_EVENT_NAME','workflow_run','wrong event'),
            ('GITHUB_SHA','3'*40,'wrong checkout'),
        ):
            with self.subTest(key=key,value=value):
                self.assertIn(error,self.gate_result(environment={key:value}))

    def test_gate_rejects_wrong_parent_tree_and_product_paths(self):
        m=json.loads((CONTROL/'source.json').read_text())
        head='1'*40
        for command,reply,error in (
            (('rev-list','--parents','-n','1',head),head+' '+m['source']+' '+m['base'],'nonlinear or wrong parent'),
            (('rev-list','--parents','-n','1',m['source']),m['source']+' '+'3'*40,'nonlinear or wrong parent'),
            (('rev-parse',m['source']+'^{tree}'),'3'*40,'reviewed product tree changed'),
            (('diff','--name-only',m['base'],m['source']),'\n'.join(m['files'])+'\ninternal/gui/assets/dictionary.js','product path drift'),
        ):
            with self.subTest(command=command):
                self.assertIn(error,self.gate_result(answers={command:reply}))

    def test_gate_rejects_control_drift_and_dirty_checkout(self):
        m=json.loads((CONTROL/'source.json').read_text())
        head='1'*40
        for command,reply,error in (
            (('diff','--name-only',m['source'],head),'.github/workflows/release.yml','unexpected validation changes'),
            (('status','--porcelain','--untracked-files=all'),'?? synthetic-extra','dirty checkout before validation'),
            (('ls-tree',head,'--','.github/dictionary-core-validation/source.json'),'120000 blob '+'2'*40,'nonregular control file'),
        ):
            with self.subTest(command=command):
                self.assertIn(error,self.gate_result(answers={command:reply}))

    def test_core_only_scope_has_no_frontend_dependency(self):
        m=json.loads((CONTROL/'source.json').read_text())
        self.assertEqual(m['base'],'d65e4026f0856215a35232fabc4cbb3f98fa52d7')
        self.assertEqual(m['branch'],'refs/heads/validation/local-dictionary-core-20261102')
        self.assertTrue(all(path.endswith('.go') and '/assets/' not in path for path in m['files']))
        self.assertEqual(sum(map(len,json.loads((CONTROL/'tests.json').read_text()).values())),42)
        self.assertFalse((CONTROL/'browser_runner.py').exists())
        self.assertFalse((CONTROL/'package.json').exists())
        self.assertFalse((CONTROL/'windows-acl.ps1').exists())
        self.assertNotIn('m[\'core\']',(CONTROL/'gate.py').read_text())
        self.assertNotIn('browser=module',(CONTROL/'owned-selftest.py').read_text())

    def test_hashes_and_syntax(self):
        hashes=json.loads((CONTROL/'control-hashes.json').read_text())
        files={p.relative_to(ROOT).as_posix() for p in (ROOT/'.github').rglob('*') if p.is_file()}
        self.assertEqual(set(hashes),files-{'.github/dictionary-core-validation/control-hashes.json'})
        for name,digest in hashes.items():
            self.assertEqual(hashlib.sha256((ROOT/name).read_bytes()).hexdigest(),digest,name)
        for p in CONTROL.rglob('*.py'):
            ast.parse(p.read_text(),filename=str(p))
        for p in CONTROL.rglob('*.json'):
            json.loads(p.read_text())

    def test_workflow_is_read_only_and_serial(self):
        text=(ROOT/'.github/workflows/local-dictionary-core-validation.yml').read_text()
        self.assertEqual(re.findall(r'^  (\w+):\s*$',text,re.M)[-2:],['core','resources'])
        self.assertEqual(re.findall(r'^    needs: (\w+)$',text,re.M),['core'])
        self.assertNotIn('contents: write',text)
        self.assertNotIn('${{ secrets.',text)
        self.assertNotIn('pull_request_target:',text)
        self.assertNotIn('sudo ',text)
        self.assertNotIn('--with-deps',text)
        self.assertNotIn('playwright',text.lower())
        self.assertNotIn('windows-latest',text)
        self.assertNotIn('setup-node@',text)
        self.assertNotRegex(text,r'\b(?:release create|git push|gh api)\b')
        self.assertEqual(set(re.findall(r'uses: (actions/[^\s]+)',text)),set(json.loads((CONTROL/'source.json').read_text())['actions']))
        self.assertEqual(text.count('persist-credentials: false'),2)

    def test_every_selected_package_has_an_offline_overlay(self):
        tree=ast.parse((CONTROL/'install_fixtures.py').read_text())
        files=next(ast.literal_eval(node.value) for node in tree.body if isinstance(node,ast.Assign) and any(isinstance(target,ast.Name) and target.id=='FILES' for target in node.targets))
        expected={'core':{'redact','localdictionary','gui','gateway','backup'},'resources':{'localdictionary'}}
        for phase,packages in expected.items():
            overlays={Path(dest).parent.name for source,dest in files[phase].items() if 'testenv.Offline()' in (CONTROL/'fixtures'/source).read_text()}
            self.assertEqual(overlays,packages)
        self.assertIn(".open('xb')",(CONTROL/'install_fixtures.py').read_text())

    def test_resource_guard_refuses_without_launch(self):
        spec=importlib.util.spec_from_file_location('dictionary_resources',CONTROL/'resources.py')
        module=importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        with mock.patch.object(module.os,'geteuid',return_value=0), mock.patch.object(module,'current_group') as probe, mock.patch.object(module.subprocess,'Popen') as launch:
            with self.assertRaisesRegex(RuntimeError,'ENVIRONMENT NOT RUN'):
                module.main()
            probe.assert_not_called(); launch.assert_not_called()
        with mock.patch.object(module.os,'geteuid',return_value=1000), mock.patch.object(module,'current_group',side_effect=PermissionError('synthetic no delegation')), mock.patch.object(module.subprocess,'Popen') as launch:
            with self.assertRaisesRegex(RuntimeError,'ENVIRONMENT NOT RUN'):
                module.main()
            launch.assert_not_called()

if __name__=='__main__':
    unittest.main()
