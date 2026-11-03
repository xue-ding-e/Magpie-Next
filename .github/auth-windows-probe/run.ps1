$ErrorActionPreference='Stop'
if ($env:GITHUB_ACTIONS -cne 'true' -or $env:RUNNER_ENVIRONMENT -cne 'github-hosted' -or $env:RUNNER_OS -cne 'Windows') { throw 'Hosted disposable Windows runner required' }
$control=Split-Path -Parent $MyInvocation.MyCommand.Path
$source=[IO.Path]::GetFullPath((Join-Path $env:GITHUB_WORKSPACE 'source'))
$report=Join-Path $env:GITHUB_WORKSPACE 'auth-windows-evidence'
New-Item -ItemType Directory -Path $report | Out-Null
$go=(Get-Command go -CommandType Application).Source
$version=(& $go version | Out-String).Trim()
if ($LASTEXITCODE -ne 0 -or $version -notmatch '^go version go1\.26\.10 windows/amd64$') { throw 'Unexpected Go toolchain' }
foreach ($name in @('GOPATH','GOMODCACHE','GOCACHE')) {
 $value=(& $go env $name | Out-String).Trim();if ($LASTEXITCODE -ne 0 -or !$value) {throw 'Cannot fix Go cache paths'}
 [Environment]::SetEnvironmentVariable($name,$value,'Process')
}
$runnerTemp=[IO.Path]::GetFullPath($env:RUNNER_TEMP).TrimEnd('\')
$owned=Join-Path $runnerTemp ('magpie-auth-win-'+[Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $owned | Out-Null
Set-Content -LiteralPath (Join-Path $owned '.owned-probe') -Value 'synthetic-only-v1' -NoNewline
$env:AUTH_WIN_OWNED_ROOT=$owned
"AUTH_WIN_OWNED_ROOT=$owned" >> $env:GITHUB_ENV
$isolatedHome=Join-Path $owned 'home';$tmp=Join-Path $owned 'tmp'
New-Item -ItemType Directory -Path $isolatedHome,$tmp,(Join-Path $isolatedHome 'Downloads') | Out-Null
foreach ($name in @('HOME','USERPROFILE','XDG_CONFIG_HOME','XDG_CACHE_HOME','XDG_DATA_HOME','XDG_STATE_HOME','APPDATA','LOCALAPPDATA')) {[Environment]::SetEnvironmentVariable($name,$isolatedHome,'Process')}
foreach ($name in @('TMP','TEMP','TMPDIR')) {[Environment]::SetEnvironmentVariable($name,$tmp,'Process')}
$env:GOTOOLCHAIN='local';$env:GOWORK='off';$env:GOMAXPROCS='2';$env:MAGPIE_AUTH_ACL_HELPER=Join-Path $control 'read-acl.ps1';$env:MAGPIE_AUTH_ALLOW_DISPOSABLE_CLIPBOARD='1'
$stdout=Join-Path $report 'go.jsonl';$stderr=Join-Path $report 'go.stderr'
$process=$null;$reaped=$false;$timedOut=$false;$code=125
try {
 $args=@('test','-mod=readonly','-count=1','-timeout=3m','-json','-run','^(TestCodexAuthExportWindowsACLProbe|TestCodexAuthWindowsClipboardAdapterProbe|TestCodexAuthWindowsACLMaskControls)$','./internal/gui')
 $process=Start-Process -FilePath $go -ArgumentList $args -WorkingDirectory $source -RedirectStandardOutput $stdout -RedirectStandardError $stderr -NoNewWindow -PassThru
 $started=$process.StartTime.ToUniversalTime()
 [ordered]@{pid=$process.Id;started=$started.ToString('o');toolchain=$version;source='a27f0957427b4ba86f7c140fa38068922adcc9ea';nativeUI=false} | ConvertTo-Json | Set-Content (Join-Path $report 'process.json')
 if (!$process.WaitForExit(1200000)) {
  $timedOut=$true
  $live=Get-Process -Id $process.Id -ErrorAction SilentlyContinue
  if ($live -and $live.StartTime.ToUniversalTime() -eq $started) { & "$env:SystemRoot/System32/taskkill.exe" /PID $process.Id /T /F | Out-Null }
  if (!$process.WaitForExit(10000)) {throw 'Owned process did not reap after timeout'}
 }
 $process.Refresh();$reaped=$process.HasExited;$code=$process.ExitCode
} finally {
 if ($process -and !$process.HasExited) {
  $live=Get-Process -Id $process.Id -ErrorAction SilentlyContinue
  if ($live -and $live.StartTime.ToUniversalTime() -eq $started) { & "$env:SystemRoot/System32/taskkill.exe" /PID $process.Id /T /F | Out-Null }
  $reaped=$process.WaitForExit(10000)
 }
 [ordered]@{exit=$code;timedOut=$timedOut;reaped=$reaped;nativeUI=false;scope='Real exporter DACL and host.Copy adapter only'} | ConvertTo-Json | Set-Content (Join-Path $report 'process-outcome.json')
}
if ($timedOut -or !$reaped) {exit 124}
exit $code
