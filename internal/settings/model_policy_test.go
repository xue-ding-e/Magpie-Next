package settings

import (
	"bytes"
	"os"
	"slices"
	"testing"
)

func TestAllowedModelsSaveNormalizesItsOwnCopy(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	models := []string{" gpt-6.1-sol ", " vendor/model "}
	before := slices.Clone(models)
	if err := Save(Settings{AllowedModels: models}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(models, before) {
		t.Fatalf("saving changed the caller's models: %q, want %q", models, before)
	}
	want := []string{"gpt-6.1-sol", "vendor/model"}
	if got := Load().AllowedModels; !slices.Equal(got, want) {
		t.Fatalf("saved models = %q, want %q", got, want)
	}
	// The new slice participates in the current upstream's deep-copy
	// reader too: changing one caller's list changes no other reader.
	read := Load()
	read.AllowedModels[0] = "another-model"
	if got := Load().AllowedModels; !slices.Equal(got, want) {
		t.Fatalf("a caller changed the shared model policy: %q", got)
	}
	for _, empty := range [][]string{nil, {}} {
		if err := Save(Settings{AllowedModels: want}); err != nil {
			t.Fatal(err)
		}
		saved := Load()
		saved.AllowedModels = empty
		if err := Save(saved); err != nil {
			t.Fatal(err)
		}
		if got := Load().AllowedModels; len(got) != 0 {
			t.Fatalf("clearing the policy left %q", got)
		}
	}
}

func TestAllowedModelsInvalidSaveLeavesPolicyAndCallerIntact(t *testing.T) {
	for _, id := range []string{"", "  ", "gpt-*", "vendor?model", "bad\nid", "bad\rid", "bad\tid", "bad\x00id", "bad\x7fid", "bad\u0085id"} {
		t.Run(id, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			if err := Save(Settings{AllowedModels: []string{"kept-model"}}); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(Path())
			if err != nil {
				t.Fatal(err)
			}
			models := []string{" trimmed-model ", id}
			caller := slices.Clone(models)
			if err := Save(Settings{AllowedModels: models}); err == nil {
				t.Fatalf("invalid exact id %q was saved", id)
			}
			if !slices.Equal(models, caller) {
				t.Fatalf("a failed save changed the caller's models: %q", models)
			}
			after, err := os.ReadFile(Path())
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, before) {
				t.Fatal("an invalid policy changed the saved settings")
			}
		})
	}
}
