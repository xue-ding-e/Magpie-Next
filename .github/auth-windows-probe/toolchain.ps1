function Select-GoApplicationPath([object[]]$Candidates) {
 if (!$Candidates -or $Candidates.Count -eq 0) {throw 'Go application not found'}
 foreach ($candidate in $Candidates) {
  if (!$candidate -or $candidate.CommandType.ToString() -cne 'Application') {throw 'Go command is not an application'}
  $path=$candidate.Source
  if ($path -isnot [string] -or [string]::IsNullOrWhiteSpace($path) -or $path -match '[\r\n]' -or ![IO.Path]::IsPathFullyQualified($path)) {throw 'Go application must resolve to one absolute path'}
  if ($path -match '(?i)(^|[\\/])(?:_?temp|tmp)([\\/]|$)') {continue}
  $insideTemp=$false
  foreach ($tempRoot in @($env:TEMP,$env:TMP,$env:RUNNER_TEMP)) {
   if ($tempRoot -and $path.StartsWith([IO.Path]::GetFullPath($tempRoot).TrimEnd('\','/')+[IO.Path]::DirectorySeparatorChar,[StringComparison]::OrdinalIgnoreCase)) {$insideTemp=$true}
  }
  if ($insideTemp) {continue}
  return $path
 }
 throw 'No non-temporary Go application found' 
}
function Assert-PinnedGoVersion([string]$Version,[int]$ExitCode) {
 if ($ExitCode -ne 0 -or $Version -cne 'go version go1.26.10 windows/amd64') {throw 'Unexpected Go toolchain'}
}
