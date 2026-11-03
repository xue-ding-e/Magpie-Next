//go:build windows

package gui

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This test-only probe calls the real exporter. It never loads saved accounts.
// ACL mutations are limited to a newly created synthetic Downloads directory.
func TestCodexAuthExportWindowsACLProbe(t *testing.T) {
	helper := os.Getenv("MAGPIE_AUTH_ACL_HELPER")
	if helper == "" {
		t.Fatal("missing frozen ACL helper")
	}
	for _, scenario := range []string{"normal-inheritance", "broad-synthetic-parent"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			downloads := filepath.Join(root, "synthetic-user", "Downloads")
			if err := os.MkdirAll(downloads, 0o700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(root, ".synthetic-auth-acl-probe")
			if err := os.WriteFile(marker, []byte("synthetic-only-v1"), 0o600); err != nil {
				t.Fatal(err)
			}
			query := func(mode, target string) map[string]any {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoProfile", "-NonInteractive", "-File", helper, "-Mode", mode)
				cmd.WaitDelay = 2 * time.Second
				cmd.Env = aclProbeEnvironment(os.Environ(), root, target)
				b, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("ACL helper failed (%s): %v: %s", mode, err, b)
				}
				var result map[string]any
				if err := json.Unmarshal(b, &result); err != nil {
					t.Fatalf("invalid ACL evidence: %v", err)
				}
				return result
			}
			if scenario == "broad-synthetic-parent" {
				query("GrantSyntheticParentRead", downloads)
			}
			before := query("Read", downloads)
			if scenario == "broad-synthetic-parent" && !aclProbeHasRead(before, "S-1-1-0") {
				t.Fatal("broad-parent fixture did not establish Everyone read")
			}
			fake := []byte(`{"synthetic":true,"tokens":{"access_token":"fake-windows-acl-only"}}`)
			path, err := saveCodexAuthExport(context.Background(), downloads, fake)
			if err != nil {
				t.Fatal(err)
			}
			relative, err := filepath.Rel(downloads, path)
			if err != nil || filepath.IsAbs(relative) || strings.HasPrefix(relative, "..") || filepath.Base(path) != "auth.json" {
				t.Fatal("export escaped synthetic Downloads")
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != string(fake) {
				t.Fatal("export changed synthetic JSON bytes")
			}
			evidence := map[string]any{"scenario": scenario, "parent": before, "directory": query("Read", filepath.Dir(path)), "file": query("Read", path)}
			encoded, _ := json.Marshal(evidence)
			t.Logf("AUTH_EXPORT_DACL_EVIDENCE %s", encoded)
			for _, kind := range []string{"directory", "file"} {
				acl := evidence[kind].(map[string]any)
				if acl["NullDacl"] == true {
					t.Errorf("AUTH_EXPORT_DACL_NULL: %s", kind)
					continue
				}
				current, ok := acl["CurrentSID"].(string)
				if !ok || current == "" || acl["OwnerSID"] != current {
					t.Errorf("AUTH_EXPORT_DACL_OWNER_UNEXPECTED: %s", kind)
					continue
				}
				rules, ok := acl["Rules"].([]any)
				if !ok {
					t.Errorf("AUTH_EXPORT_DACL_RULES_MISSING: %s", kind)
					continue
				}
				for _, item := range rules {
					rule := item.(map[string]any)
					sid, _ := rule["SID"].(string)
					if sid == current || sid == "S-1-5-18" || sid == "S-1-5-32-544" {
						continue
					}
					if rule["Type"] == "Allow" && rule["AppliesHere"] == true && aclProbeRights(rule["Rights"])&(1|0x40000|0x80000|0xf0000000) != 0 {
						// A strict owner/SYSTEM/Administrators policy check, not an impersonation
						// or effective-access proof. Preserve the complete deny/allow evidence.
						t.Errorf("AUTH_EXPORT_DACL_NON_OWNER_ALLOW: %s SID=%s", kind, sid)
					}
				}
			}
		})
	}
}

func aclProbeHasRead(acl map[string]any, sid string) bool {
	rules, ok := acl["Rules"].([]any)
	if !ok {
		return false
	}
	for _, item := range rules {
		r := item.(map[string]any)
		if r["SID"] == sid && r["Type"] == "Allow" && r["AppliesHere"] == true && aclProbeRights(r["Rights"])&(1|0x80000000|0x10000000) != 0 {
			return true
		}
	}
	return false
}

// Windows ACCESS_MASK is uint32. PowerShell/.NET may serialize its high
// generic bits as a signed Int32 or a positive Int64. Reject malformed values.
// Strict policy rejects every unmapped generic bit conservatively; HasRead
// recognizes only GENERIC_READ/GENERIC_ALL (not generic write/execute).
func aclProbeRights(value any) uint32 {
	n, ok := value.(float64)
	if !ok || n < -2147483648 || n > 4294967295 || n != float64(int64(n)) {
		panic("invalid Windows access mask evidence")
	}
	return uint32(int64(n))
}

func TestCodexAuthWindowsACLMaskControls(t *testing.T) {
	for _, n := range []float64{2147483648, -2147483648, 2147483649, -2147483647, 4026531840, -268435456, 4294967295, -1} {
		if aclProbeRights(n)&0x80000000 == 0 {
			t.Fatal("generic read lost during uint32 normalization")
		}
		acl := map[string]any{"Rules": []any{map[string]any{"SID": "S-1-1-0", "Type": "Allow", "AppliesHere": true, "Rights": n}}}
		if !aclProbeHasRead(acl, "S-1-1-0") {
			t.Fatal("generic read not recognized")
		}
	}
	for _, n := range []float64{1073741824, 536870912, 1610612736} {
		rights := aclProbeRights(n)
		if rights&0xf0000000 == 0 || rights&(1|0x80000000|0x10000000) != 0 {
			t.Fatal("generic write/execute confused with read")
		}
	}
	for _, bad := range []any{float64(-2147483649), float64(4294967296), 1.5, true, "2147483648"} {
		rejected := false
		func() {
			defer func() {
				if recover() != nil {
					rejected = true
				}
			}()
			aclProbeRights(bad)
		}()
		if !rejected {
			t.Fatal("malformed access mask accepted")
		}
	}
	for _, input := range [][]string{
		{"PATH=synthetic-path", "PSModulePath=synthetic-ps7", "HOME=synthetic-home"},
		{"PATH=synthetic-path", "psmodulepath=synthetic-ps7", "PSMODULEPATH=duplicate", "AUTH_ACL_ROOT=old", "auth_acl_target=old", "HOME=synthetic-home"},
		{"PATH=synthetic-path", "HOME=synthetic-home"},
	} {
		got := aclProbeEnvironment(input, "synthetic-root", "synthetic-target")
		want := "PATH=synthetic-path|HOME=synthetic-home|AUTH_ACL_ROOT=synthetic-root|AUTH_ACL_TARGET=synthetic-target"
		if strings.Join(got, "|") != want {
			t.Fatal("child environment did not isolate Windows PowerShell module resolution")
		}
	}

}

// Windows PowerShell launched through an intermediate Go process otherwise
// inherits PowerShell7 module paths. Clear only this child variable so the
// unchanged Windows PowerShell host builds its matching default module paths.
func aclProbeEnvironment(base []string, root, target string) []string {
	out := make([]string, 0, len(base)+2)
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, "PSModulePath") || strings.EqualFold(key, "AUTH_ACL_ROOT") || strings.EqualFold(key, "AUTH_ACL_TARGET") {
			continue
		}
		out = append(out, entry)
	}
	return append(out, "AUTH_ACL_ROOT="+root, "AUTH_ACL_TARGET="+target)
}
