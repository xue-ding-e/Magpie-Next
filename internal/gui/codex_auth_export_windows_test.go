package gui

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestCodexAuthExportWindowsPrivateAtCreation(t *testing.T) {
	// Only this new test directory receives permissive permissions. Never
	// modify the ACL of TEMP, Downloads, a real profile, or an existing path.
	parent := filepath.Join(t.TempDir(), "synthetic-downloads")
	sd, err := windows.SecurityDescriptorFromString("D:(A;OICI;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	sa := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	name, err := windows.UTF16PtrFromString(parent)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.CreateDirectory(name, sa); err != nil {
		t.Fatal(err)
	}

	dir, release, err := makeCodexAuthExportDirectory(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	// The directory is private while it is still empty, not tightened after
	// credentials have already been written.
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("new private directory is not empty")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	check := func(path string, inherit bool) {
		t.Helper()
		sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		owner, _, err := sd.Owner()
		if err != nil || owner == nil || !owner.Equals(user.User.Sid) {
			t.Fatal("export owner is not the process user")
		}
		control, _, err := sd.Control()
		if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
			t.Fatal("export inherits its permissive parent ACL")
		}
		acl, _, err := sd.DACL()
		if err != nil || acl == nil || acl.AceCount != 3 {
			t.Fatal("unexpected export ACL")
		}
		for i := uint32(0); i < uint32(acl.AceCount); i++ {
			var ace *windows.ACCESS_ALLOWED_ACE
			if err := windows.GetAce(acl, i, &ace); err != nil {
				t.Fatal(err)
			}
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()
			if sid != user.User.Sid.String() && sid != "S-1-5-18" && sid != "S-1-5-32-544" {
				t.Fatal("export grants another principal access")
			}
			flags := byte(0)
			if inherit {
				flags = windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE
			}
			if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags != flags {
				t.Fatal("export security was inherited")
			}
		}
	}
	check(dir, true)
	release()
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	path, err := saveCodexAuthExport(context.Background(), parent, []byte(`{"synthetic":true}`))
	if err != nil {
		t.Fatal(err)
	}
	check(filepath.Dir(path), true)
	check(path, false)
	if b, err := os.ReadFile(path); err != nil || string(b) != `{"synthetic":true}` {
		t.Fatal("export content changed")
	}
}

func TestCodexAuthExportWindowsCreationFailure(t *testing.T) {
	parent := t.TempDir()
	if path, err := saveCodexAuthExport(context.Background(), filepath.Join(parent, "missing", "downloads"), []byte(`{"synthetic":true}`)); err == nil || path != "" {
		t.Fatal("missing parent unexpectedly succeeded")
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatal("failed creation left export data")
	}
}
