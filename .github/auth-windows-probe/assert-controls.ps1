# Parse, never execute, synthetic examples and checked script bodies.
$ErrorActionPreference='Stop'
$readOnlyNames=@('HOME','PID','PSHOME','HOST','EXECUTIONCONTEXT','PSVERSIONTABLE','SHELLID','TRUE','FALSE','NULL','PSCULTURE','PSUICULTURE','PSEDITION')
function Get-ReadOnlyAssignments([string]$Source) {
 $tokens=$null;$parseErrors=$null
 $tree=[Management.Automation.Language.Parser]::ParseInput($Source,[ref]$tokens,[ref]$parseErrors)
 if ($parseErrors.Count) {throw 'PowerShell syntax error in static control'}
 $found=[Collections.Generic.List[string]]::new()
 function Check-Target($Target) {
  if ($Target -is [Management.Automation.Language.VariableExpressionAst]) {
   $path=$Target.VariablePath.UserPath
   # Environment variables are not PowerShell's automatic variables.
   if ($path -match '^(?i:env):') {return}
   $name=$path -replace '^(?i:local|global|script|private):',''
   if ($readOnlyNames -contains $name.ToUpperInvariant()) {$found.Add($name.ToUpperInvariant())}
  } elseif ($Target -is [Management.Automation.Language.ArrayLiteralAst]) {
   foreach ($element in $Target.Elements) {Check-Target $element}
  }
 }
 foreach ($node in $tree.FindAll({param($n) $n -is [Management.Automation.Language.AssignmentStatementAst]},$true)) {Check-Target $node.Left}
 foreach ($node in $tree.FindAll({param($n) $n -is [Management.Automation.Language.UnaryExpressionAst]},$true)) {
  if ($node.TokenKind.ToString() -in @('PlusPlus','MinusMinus','PostfixPlusPlus','PostfixMinusMinus')) {Check-Target $node.Child}
 }
 foreach ($node in $tree.FindAll({param($n) $n -is [Management.Automation.Language.ForEachStatementAst]},$true)) {Check-Target $node.Variable}
 foreach ($node in $tree.FindAll({param($n) $n -is [Management.Automation.Language.ParameterAst]},$true)) {Check-Target $node.Name}
 return @($found)
}
$bad=@('$home=1','$PID = 1','${HoMe} = 1','$script:HOME += 1','$local:pid++','$x,$HOME=1,2','foreach($HOME in 1){ }','param($PID)','"prefix $($HOME=1) suffix"')
foreach ($sample in $bad) {if (@(Get-ReadOnlyAssignments $sample).Count -eq 0) {throw 'Forbidden actual assignment was missed'}}
$good=@('$isolatedHome = "owned"','$env:HOME = "synthetic"','Write-Output $HOME','$x = $PID','# $HOME = 1',"'`$HOME = 1'",'"$HOME = 1"','<# $PID = 1 #> $x = 1','$obj.HOME = 1','$x = ($HOME -eq "read")')
foreach ($sample in $good) {if (@(Get-ReadOnlyAssignments $sample).Count -ne 0) {throw 'Read/comment/string/environment write misclassified'}}
$directory=Split-Path -Parent $MyInvocation.MyCommand.Path
foreach ($name in @('run.ps1','cleanup.ps1','read-acl.ps1','assert-controls.ps1','toolchain.ps1')) {
 $source=Get-Content -LiteralPath (Join-Path $directory $name) -Raw
 if (@(Get-ReadOnlyAssignments $source).Count -ne 0) {throw "Actual read-only variable assignment in $name"}
}
Write-Output '19 PowerShell AST assignment positive/negative controls and five actual scripts passed; no fixture code executed'
. (Join-Path $directory 'toolchain.ps1')
$new=[pscustomobject]@{CommandType='Application';Source='C:\hostedtoolcache\windows\go\1.26.10\x64\bin\go.exe'}
$old=[pscustomobject]@{CommandType='Application';Source='C:\hostedtoolcache\windows\go\1.24.13\x64\bin\go.exe'}
if ((Select-GoApplicationPath @($new,$old)) -cne $new.Source) {throw 'Multiple Go matches were not reduced to first path'}
if ((Select-GoApplicationPath @($new)) -cne $new.Source) {throw 'Single Go match not retained'}
$toolchainChecks=2
$badCandidates=@(
 [pscustomobject]@{Items=@()},
 [pscustomobject]@{Items=@([pscustomobject]@{CommandType='Application';Source=@('C:\a.exe','C:\b.exe')})},
 [pscustomobject]@{Items=@([pscustomobject]@{CommandType='Application';Source=''})},
 [pscustomobject]@{Items=@([pscustomobject]@{CommandType='Application';Source="C:\a.exe`nC:\b.exe"})},
 [pscustomobject]@{Items=@([pscustomobject]@{CommandType='Application';Source='relative.exe'})},
 [pscustomobject]@{Items=@([pscustomobject]@{CommandType='Function';Source='C:\a.exe'})}
)
foreach ($bad in $badCandidates) {
 $rejected=$false
 try {Select-GoApplicationPath $bad.Items | Out-Null} catch {$rejected=$true}
 if (!$rejected) {throw 'Invalid command resolution accepted'}
 $toolchainChecks++
}
Assert-PinnedGoVersion 'go version go1.26.10 windows/amd64' 0
$toolchainChecks++
foreach ($case in @([pscustomobject]@{Version='go version go1.24.13 windows/amd64';Exit=0},[pscustomobject]@{Version='';Exit=0},[pscustomobject]@{Version='go version go1.26.10 windows/amd64';Exit=1})) {
 $rejected=$false
 try {Assert-PinnedGoVersion $case.Version $case.Exit} catch {$rejected=$true}
 if (!$rejected) {throw 'Unpinned or failed Go version accepted'}
 $toolchainChecks++
}
$temporary=[pscustomobject]@{CommandType='Application';Source='C:\Users\synthetic\AppData\Local\Temp\go.exe'}
if ((Select-GoApplicationPath @($temporary,$new)) -cne $new.Source) {throw 'Temporary Go was selected'}
$rejected=$false
try {Select-GoApplicationPath @($temporary) | Out-Null} catch {$rejected=$true}
if (!$rejected) {throw 'Temporary-only Go accepted'}
$toolchainChecks+=2
if ($toolchainChecks -ne 14) {throw 'Toolchain control count mismatch'}
Write-Output '14 PowerShell scalar-command, non-temp and pinned-version controls passed; no Go process executed'  
