package provider

import (
	"errors"
	"os"
	"testing"

	"github.com/yetone/magpie/internal/settings"
)

func TestModelPolicyRefusalsAreLocal(t *testing.T) {
	for _, contents := range []string{
		`{"allowedModels":["another-model"]}`,
		`{"allowedModels":["*"]}`,
		`{`,
	} {
		t.Run(contents, func(t *testing.T) {
			modelPolicyHome(t, nil)
			if err := os.Remove(settings.Path() + ".bak"); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if err := os.WriteFile(settings.Path(), []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := RequireAllowedModel("claude-haiku-4-5"); !errors.Is(err, ErrModelPolicy) {
				t.Fatalf("a policy refusal lost its local classification: %v", err)
			}
			if err := RequireAllowedClaudeModel("claude-haiku-4-5"); !errors.Is(err, ErrModelPolicy) {
				t.Fatalf("a Claude policy refusal lost its local classification: %v", err)
			}
		})
	}
}

func TestModelPolicyClaudeAliasCannotNameExactModel(t *testing.T) {
	modelPolicyHome(t, []string{"haiku"})
	if err := RequireAllowedClaudeModel("haiku"); !errors.Is(err, ErrModelPolicy) {
		t.Fatalf("allowing an unresolved CLI alias opened the exact-model policy: %v", err)
	}
}
