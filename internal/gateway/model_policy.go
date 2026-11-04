package gateway

import (
	"context"
	"sync/atomic"

	"github.com/yetone/magpie/internal/provider"
)

type modelPolicyKey struct{}

// checkModelPolicy distinguishes a local refusal from an upstream failure.
// A refused candidate must not put an otherwise healthy account to rest.
func checkModelPolicy(ctx context.Context, model string) error {
	err := provider.RequireAllowedModel(model)
	if err != nil {
		if denied, ok := ctx.Value(modelPolicyKey{}).(*atomic.Bool); ok {
			denied.Store(true)
		}
	}
	return err
}
