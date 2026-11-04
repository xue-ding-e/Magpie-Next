package provider

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/yetone/magpie/internal/settings"
)

// ErrModelPolicy identifies a local refusal, never a vendor or account fault.
var ErrModelPolicy = errors.New("outbound model requests are blocked")

// RequireAllowedModel checks the user's exact outbound model allowlist.
// An empty list preserves unrestricted routing. An unreadable or invalid
// policy is unknown, so it never permits an outbound model request.
func RequireAllowedModel(model string) error {
	return requireAllowedModel(model, true)
}

// RequireAllowedClaudeModel also refuses Claude Code aliases under an
// allowlist: only the CLI knows what model haiku or sonnet would select.
// With no policy, those aliases keep their existing meaning.
func RequireAllowedClaudeModel(model string) error {
	return requireAllowedModel(model, strings.HasPrefix(model, "claude-"))
}

func requireAllowedModel(model string, exact bool) error {
	s, err := settings.LoadChecked()
	if err != nil {
		return fmt.Errorf("%w: allowedModels could not be read from settings.json", ErrModelPolicy)
	}
	for _, id := range s.AllowedModels {
		if strings.TrimSpace(id) == "" || strings.ContainsAny(id, "*?\r\n\t") {
			return fmt.Errorf("%w: allowedModels in settings.json contains an invalid exact model id", ErrModelPolicy)
		}
	}
	if len(s.AllowedModels) > 0 && !exact {
		return fmt.Errorf("%w: Claude Code alias %q does not establish an exact model for allowedModels", ErrModelPolicy, model)
	}
	if len(s.AllowedModels) > 0 && !slices.Contains(s.AllowedModels, model) {
		return fmt.Errorf("%w: model %q is not allowed by magpie's allowedModels setting", ErrModelPolicy, model)
	}
	return nil
}
