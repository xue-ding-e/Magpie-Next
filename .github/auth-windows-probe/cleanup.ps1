$ErrorActionPreference='Stop'
if ($env:GITHUB_ACTIONS -cne 'true' -or $env:RUNNER_ENVIRONMENT -cne 'github-hosted' -or $env:RUNNER_OS -cne 'Windows') { throw 'Hosted disposable Windows runner required' }
if (!$env:AUTH_WIN_OWNED_ROOT) {Write-Output 'Owned root was not created';exit 0}
$root=[IO.Path]::GetFullPath($env:AUTH_WIN_OWNED_ROOT)
$parent=[IO.Path]::GetFullPath($env:RUNNER_TEMP).TrimEnd('\')
if ([IO.Path]::GetDirectoryName($root) -cne $parent -or [IO.Path]::GetFileName($root) -notmatch '^magpie-auth-win-[0-9a-f]{32}$') {throw 'Not an owned probe directory'}
if (Test-Path -LiteralPath $root) {
 $item=Get-Item -LiteralPath $root -Force
 if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) {throw 'Refuse reparse cleanup root'}
 if ((Get-Content -LiteralPath (Join-Path $root '.owned-probe') -Raw) -cne 'synthetic-only-v1') {throw 'Owned marker missing'}
 $links=@(Get-ChildItem -LiteralPath $root -Force -Recurse | Where-Object {$_.Attributes -band [IO.FileAttributes]::ReparsePoint})
 if ($links.Count) {throw 'Refuse recursive cleanup with reparse entries'}
 Remove-Item -LiteralPath $root -Recurse -Force
 if (Test-Path -LiteralPath $root) {throw 'Owned cleanup incomplete'}
}
Write-Output 'Owned synthetic profile/temp removed; no other profile or clipboard touched by cleanup step'
