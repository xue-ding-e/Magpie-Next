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
foreach ($name in @('run.ps1','cleanup.ps1','read-acl.ps1','assert-controls.ps1')) {
 $source=Get-Content -LiteralPath (Join-Path $directory $name) -Raw
 if (@(Get-ReadOnlyAssignments $source).Count -ne 0) {throw "Actual read-only variable assignment in $name"}
}
Write-Output '19 PowerShell AST assignment positive/negative controls and four actual scripts passed; no fixture code executed'
