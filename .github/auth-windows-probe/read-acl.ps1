param([ValidateSet('Read','GrantSyntheticParentRead')][string]$Mode='Read')
$ErrorActionPreference='Stop'
$root=[IO.Path]::GetFullPath($env:AUTH_ACL_ROOT)
$target=[IO.Path]::GetFullPath($env:AUTH_ACL_TARGET)
if (!$root -or !$target.StartsWith($root + [IO.Path]::DirectorySeparatorChar,[StringComparison]::OrdinalIgnoreCase)) { throw 'Target outside owned synthetic root' }
if ((Get-Content -LiteralPath (Join-Path $root '.synthetic-auth-acl-probe') -Raw) -cne 'synthetic-only-v1') { throw 'Synthetic marker missing' }
$current=Get-Item -LiteralPath $target -Force
while ($current) {
 if ($current.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Reparse point not allowed in ACL probe path' }
 $current=if ($current -is [IO.FileInfo]) { $current.Directory } else { $current.Parent }
}
$downloads=Join-Path $root 'synthetic-user/Downloads'
if ($Mode -eq 'GrantSyntheticParentRead') {
 if ($target -cne [IO.Path]::GetFullPath($downloads) -or !(Test-Path -LiteralPath $target -PathType Container)) { throw 'Only the exact newly-created synthetic Downloads parent may be changed' }
 $acl=Get-Acl -LiteralPath $target
 $sid=[Security.Principal.SecurityIdentifier]::new('S-1-1-0')
 $rule=[Security.AccessControl.FileSystemAccessRule]::new($sid,[Security.AccessControl.FileSystemRights]::Read,[Security.AccessControl.InheritanceFlags]'ContainerInherit,ObjectInherit',[Security.AccessControl.PropagationFlags]::None,[Security.AccessControl.AccessControlType]::Allow)
 $acl.AddAccessRule($rule)
 Set-Acl -LiteralPath $target -AclObject $acl
}
$acl=Get-Acl -LiteralPath $target
$raw=[Security.AccessControl.RawSecurityDescriptor]::new($acl.GetSecurityDescriptorBinaryForm(),0)
$rules=@($acl.GetAccessRules($true,$true,[Security.Principal.SecurityIdentifier]) | ForEach-Object {
 [ordered]@{SID=$_.IdentityReference.Value;Type=$_.AccessControlType.ToString();Rights=[int64]$_.FileSystemRights;Inherited=$_.IsInherited;Inheritance=$_.InheritanceFlags.ToString();Propagation=$_.PropagationFlags.ToString();AppliesHere=(($_.PropagationFlags -band [Security.AccessControl.PropagationFlags]::InheritOnly) -eq 0)}
})
[ordered]@{OwnerSID=$acl.GetOwner([Security.Principal.SecurityIdentifier]).Value;CurrentSID=[Security.Principal.WindowsIdentity]::GetCurrent().User.Value;Protected=$acl.AreAccessRulesProtected;NullDacl=($null -eq $raw.DiscretionaryAcl);SDDL=$acl.Sddl;Rules=$rules} | ConvertTo-Json -Depth 6 -Compress
