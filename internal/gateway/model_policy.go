package gateway

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/yetone/magpie/internal/provider"
)

type modelPolicyKey struct{}

// checkModelPolicy distinguishes a local refusal from an upstream failure.
// A refused candidate must not put an otherwise healthy account to rest.
func checkModelPolicy(ctx context.Context, model string) error {
	return modelPolicyResult(ctx, provider.RequireAllowedModel(model))
}

// Claude's interactive startup chooses its own quota model before the
// requested turn. An exact policy cannot establish that model, so this
// mode is refused locally rather than silently changing to print mode.
func checkClaudeModelPolicy(ctx context.Context, model string) error {
	err := provider.RequireAllowedClaudeModel(model)
	if err == nil && claudeTUIOn() && provider.RequireAllowedModel("") != nil {
		err = fmt.Errorf("%w: Claude Code's interactive startup selects a quota model that allowedModels cannot verify; turn off interactive mode to use an exact model policy", provider.ErrModelPolicy)
	}
	return modelPolicyResult(ctx, err)
}

func modelPolicyResult(ctx context.Context, err error) error {
	if err != nil {
		if denied, ok := ctx.Value(modelPolicyKey{}).(*atomic.Bool); ok {
			denied.Store(true)
		}
	}
	return err
}
