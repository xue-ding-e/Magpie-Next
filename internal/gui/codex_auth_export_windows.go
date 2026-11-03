package gui

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"github.com/yetone/magpie/internal/steady"
	"golang.org/x/sys/windows"
)

// Supply a protected descriptor at creation, not a later chmod/Set-Acl.
// A permissive Downloads parent must never grant access to credential bytes.
func codexAuthSecurity(inherit bool) (*windows.SecurityAttributes, string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, "", err
	}
	sid := user.User.Sid.String()
	flags := ""
	if inherit {
		flags = "OICI"
	}
	sd, err := windows.SecurityDescriptorFromString("O:" + sid + "D:P(A;" + flags + ";FA;;;" + sid + ")(A;" + flags + ";FA;;;SY)(A;" + flags + ";FA;;;BA)")
	if err != nil {
		return nil, "", err
	}
	return &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}, sid, nil
}

// Read the actual descriptor before writing. Filesystems that cannot retain
// the requested owner/protected ACL fail closed rather than exporting publicly.
func checkCodexAuthSecurity(h windows.Handle, sid string, inherit bool) error {
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	control, _, err := sd.Control()
	if err != nil {
		return err
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if owner == nil || owner.String() != sid || control&windows.SE_DACL_PROTECTED == 0 || acl == nil || acl.AceCount != 3 {
		return errors.New("Could not create a private auth export")
	}
	flags := byte(0)
	if inherit {
		flags = windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE
	}
	seen := map[string]bool{}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			return err
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Mask != windows.STANDARD_RIGHTS_REQUIRED|windows.SYNCHRONIZE|0x1ff || ace.Header.AceFlags != flags {
			return errors.New("Could not create a private auth export")
		}
		who := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()
		if (who != sid && who != "S-1-5-18" && who != "S-1-5-32-544") || seen[who] {
			return errors.New("Could not create a private auth export")
		}
		seen[who] = true
	}
	return nil
}

func codexAuthRandomPath(parent, prefix string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return filepath.Join(parent, prefix+hex.EncodeToString(b[:])), nil
}

func makeCodexAuthExportDirectory(parent string) (string, func(), error) {
	sa, sid, err := codexAuthSecurity(true)
	if err != nil {
		return "", nil, err
	}
	for attempt := 0; attempt < 16; attempt++ {
		dir, err := codexAuthRandomPath(parent, "magpie-codex-")
		if err != nil {
			return "", nil, err
		}
		name, err := windows.UTF16PtrFromString(dir)
		if err != nil {
			return "", nil, err
		}
		err = windows.CreateDirectory(name, sa)
		runtime.KeepAlive(sa)
		if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			continue
		}
		if err != nil {
			return "", nil, err
		}
		// Deny delete sharing while the export is in progress. Open the object
		// itself, never follow a substituted reparse point.
		h, err := windows.CreateFile(name, windows.READ_CONTROL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if err != nil {
			_ = os.Remove(dir)
			return "", nil, err
		}
		release := func() {
			if h != windows.InvalidHandle {
				_ = windows.CloseHandle(h)
				h = windows.InvalidHandle
			}
		}
		var info windows.ByHandleFileInformation
		err = windows.GetFileInformationByHandle(h, &info)
		if err == nil && info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			err = errors.New("Auth export directory is a reparse point")
		}
		if err == nil {
			err = checkCodexAuthSecurity(h, sid, true)
		}
		if err != nil {
			release()
			_ = os.Remove(dir)
			return "", nil, err
		}
		return dir, release, nil
	}
	return "", nil, errors.New("Could not create a unique auth export")
}

func writeCodexAuthExport(path string, b []byte) error {
	sa, sid, err := codexAuthSecurity(false)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 16; attempt++ {
		tmp, err := codexAuthRandomPath(filepath.Dir(path), ".auth.json.")
		if err != nil {
			return err
		}
		name, err := windows.UTF16PtrFromString(tmp)
		if err != nil {
			return err
		}
		h, err := windows.CreateFile(name, windows.GENERIC_WRITE|windows.READ_CONTROL, 0, sa, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
		runtime.KeepAlive(sa)
		if errors.Is(err, windows.ERROR_FILE_EXISTS) {
			continue
		}
		if err != nil {
			return err
		}
		f := os.NewFile(uintptr(h), tmp)
		err = checkCodexAuthSecurity(h, sid, false)
		if err == nil {
			_, err = f.Write(b)
		}
		if err == nil {
			err = steady.Sync(f)
		}
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err == nil {
			err = steady.Rename(tmp, path)
		}
		if err != nil {
			_ = os.Remove(tmp)
		}
		return err
	}
	return errors.New("Could not create a unique auth export file")
}
