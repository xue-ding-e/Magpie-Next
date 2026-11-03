import importlib.util,subprocess,tempfile,os
from pathlib import Path
s=importlib.util.spec_from_file_location('impact',Path(__file__).with_name('next-package-impact.py'));m=importlib.util.module_from_spec(s);s.loader.exec_module(m)
checks=0
def raw(path,status=b'M',before=b'100644',after=b'100644'):
 old=b'0'*40 if status==b'A' else b'a'*40;new=b'0'*40 if status==b'D' else b'b'*40
 return b':'+before+b' '+after+b' '+old+b' '+new+b' '+status+b'\0'+path+b'\0'
def expect(data,result):
 global checks
 assert m.classify(data)[:2]==result;checks+=1
def reject(data):
 global checks
 try:m.classify(data)
 except AssertionError:checks+=1;return
 raise AssertionError('Uncertain diff accepted')
test=raw(b'internal/gui/tests/proto-menu-overflow.test.cjs');expect(test,(False,False));expect(b'',(False,False))
ci=b''.join(raw(p) for p in sorted(m.CI));expect(ci,(True,False));expect(ci+test,(True,False));expect(ci+raw(b'go.mod'),(True,True))
for p in m.CI:
 expect(raw(p,b'A',b'000000'),(True,False));reject(raw(p,b'D',after=b'000000'))
for p in [b'main.go',b'internal/gui/web.go',b'internal/gui/assets/app.js',b'build/windows/winres.json',b'go.mod',b'go.sum',b'LICENSE']:
 expect(raw(p),(True,True))
expect(raw(b'internal/new.go',b'A',b'000000'),(True,True));expect(raw(b'internal/old.go',b'D',after=b'000000'),(True,True))
for p in [b'README.md',b'build/unknown.py',b'.github/workflows/other.yml',b'internal/gui/tests/reader.cjs',b'internal/gui/tests/a.test.cjs/evil',b'internal/gui/tests/a.test.cjs.extra']:
 reject(raw(p))
for data in [raw(b'internal/gui/tests/a.test.cjs',b'D',after=b'000000'),raw(b'internal/gui/tests/a.test.cjs',after=b'100755'),raw(b'main.go',b'T',after=b'120000'),b'bad',raw(b'a',status=b'R'),raw(b'a\nb'),raw(b''),raw(b'internal/../secret.go')]:reject(data)
with tempfile.TemporaryDirectory(prefix='impact-git.') as d:
 def git(*args):return subprocess.check_output(['git',*args],cwd=d,stderr=subprocess.PIPE)
 git('init','-q');p=Path(d)/'internal/gui/tests/a.test.cjs';p.parent.mkdir(parents=True);p.write_text('old\n');git('add','.');before=git('write-tree').decode().strip();p.write_text('new\n');git('add','.');after=git('write-tree').decode().strip();expect(git('diff','--raw','--no-abbrev','-z','--no-renames',before,after,'--'),(False,False))
 p=Path(d)/'go.mod';p.write_text('module synthetic\n');git('add','.');after=git('write-tree').decode().strip();expect(git('diff','--raw','--no-abbrev','-z','--no-renames',before,after,'--'),(True,True))
# Exercise real main output semantics against synthetic git evidence, no repository credentials.
with tempfile.TemporaryDirectory(prefix='impact-main.') as d:
 oldenv=dict(os.environ);realgit=m.git;head='b'*40
 try:
  os.environ.update(GITHUB_REPOSITORY='xue-ding-e/Magpie-Next',GITHUB_SHA=head,GITHUB_REF='refs/heads/main',BEFORE_SHA='a'*40,MANUAL_PUBLISH='false')
  def mockgit(*args):
   if args[0]=='rev-parse':return ((('a'*40) if args[1]=='HEAD^' else head)+'\n').encode()
   if args[0] in ('status','cat-file'):return b''
   if args[0]=='diff':return ci+test
   raise AssertionError('Unexpected Git call')
  m.git=mockgit
  for event,manual,published in [('push','false','false'),('workflow_dispatch','false','false'),('workflow_dispatch','true','true')]:
   out=Path(d)/f'{event}-{manual}';os.environ.update(GITHUB_EVENT_NAME=event,MANUAL_PUBLISH=manual,GITHUB_OUTPUT=str(out));m.main();assert out.read_text()==f'package=true\npublish={published}\ngui_required=true\ngui_tests=["internal/gui/tests/proto-menu-overflow.test.cjs"]\n';checks+=1
  for before in ['', '0'*40,'not-a-sha']:
   out=Path(d)/'rejected';os.environ.update(GITHUB_EVENT_NAME='push',BEFORE_SHA=before,GITHUB_OUTPUT=str(out))
   try:m.main()
   except AssertionError:assert not out.exists();checks+=1
   else:raise AssertionError('Invalid before accepted')
 finally:os.environ.clear();os.environ.update(oldenv);m.git=realgit
for path in m.DOCS:expect(raw(path),(False,False))
for path in [b'internal/gui/codex_auth_export_test.go',b'internal/provider/codex_auth_export_test.go']:
 expect(raw(path,b'A',b'000000'),(False,False));reject(raw(path,b'D',after=b'000000'))
newgui=raw(b'internal/gui/tests/new-auth.test.cjs',b'A',b'000000');expect(newgui,(False,False));assert m.classify(newgui)[3]==['internal/gui/tests/new-auth.test.cjs'];checks+=1
expect(newgui+raw(b'internal/gui/assets/app.js'),(True,True));assert m.classify(newgui+ci)[3]==['internal/gui/tests/new-auth.test.cjs'];checks+=1
reject(b''.join(raw(f'internal/gui/tests/new{i}.test.cjs'.encode()) for i in range(9)))
print(checks,'synthetic impact checks passed, including real Git trees and main output gates')
