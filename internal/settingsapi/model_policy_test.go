package settingsapi

import (
	"slices"
	"testing"

	"github.com/yetone/magpie/internal/settings"
)

// Both the page and magpie settings save through Apply. A partial save
// must keep the policy, even when its body names an empty or different
// allowedModels: that setting is carried from the current file.
func TestPartialSettingsSaveKeepsOutboundModelPolicy(t *testing.T) {
	for _, body := range []string{
		`{"theme":"dark"}`,
		`{"theme":"dark","allowedModels":[]}`,
		`{"theme":"dark","allowedModels":["another-model"]}`,
	} {
		t.Run(body, func(t *testing.T) {
			home(t)
			want := []string{"gpt-6.1-sol", "vendor/model"}
			if err := settings.Save(settings.Settings{Theme: "light", AllowedModels: want}); err != nil {
				t.Fatal(err)
			}
			in, _, err := Apply([]byte(body), false)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(in.AllowedModels, want) {
				t.Fatalf("partial save returned policy %q, want %q", in.AllowedModels, want)
			}
			got := settings.Load()
			if got.Theme != "dark" || !slices.Equal(got.AllowedModels, want) {
				t.Fatalf("partial save: theme=%q policy=%q", got.Theme, got.AllowedModels)
			}
		})
	}
}
