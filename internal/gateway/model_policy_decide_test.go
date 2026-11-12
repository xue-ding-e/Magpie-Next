package gateway

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

func TestModelPolicyClassifierRecoversWithoutCooldown(t *testing.T) {
	fresh(t)
	resetClassified(t)
	var fleet = &decideFleet{status: map[string]int{}}
	up := fleet.serve()
	t.Cleanup(up.Close)
	if err := provider.Save(provider.Provider{ID: "policy-classifier", Name: "Policy classifier", Key: "synthetic", Decide: up.URL + "/v1"}); err != nil {
		t.Fatal(err)
	}
	s := New()
	model := "policy-classifier/jev-latest"
	allowOnly(t, "another-model")
	if _, _, err := classify(s.askClassifier, model, []string{"bug"}, before{}, false, "fix this"); !errors.Is(err, provider.ErrModelPolicy) {
		t.Fatalf("expected local policy refusal: %v", err)
	}
	allowOnly(t, "jev-latest")
	if _, cached, err := classify(s.askClassifier, model, []string{"bug"}, before{}, false, "fix this"); err != nil || cached {
		t.Fatalf("local policy change delayed classifier recovery: cached=%v err=%v", cached, err)
	}
	if got := fleet.took(); len(got) != 1 {
		t.Fatalf("recovered classifier asked %v, want one request", got)
	}
}

// A local policy refusal is neither an unreachable decision provider nor
// an exhausted key, at the public endpoint and as a routing classifier.
func TestModelPolicyDecisionDenialDoesNotRest(t *testing.T) {
	for _, surface := range []string{"systemone", "classifier"} {
		t.Run(surface, func(t *testing.T) {
			fresh(t)
			var fleet = &decideFleet{status: map[string]int{}}
			up := fleet.serve()
			t.Cleanup(up.Close)
			var p = provider.Provider{ID: "policy-jev", Name: "Policy Jev", Key: "first", Keys: []provider.KeyAccount{{Key: "second"}}, Decide: up.URL + "/v1"}
			if err := provider.Save(p); err != nil {
				t.Fatal(err)
			}
			s := New()
			ask := func() (int, string) {
				if surface == "classifier" {
					_, err := s.askClassifier("policy-jev/jev-latest", []string{"bug"}, before{}, false, "fix this")
					if err != nil {
						return 0, err.Error()
					}
					return http.StatusOK, ""
				}
				w := httptest.NewRecorder()
				r := httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(`{"model":"policy-jev/jev-latest","state":{"message":"hi"},"questions":{"intent":{"type":"choice"}}}`))
				r.Header.Set("Authorization", "Bearer magpie")
				s.Handler().ServeHTTP(w, r)
				return w.Code, w.Body.String()
			}
			allowOnly(t, "another-model")
			status, message := ask()
			if !strings.Contains(message, "allowedModels") || surface == "systemone" && status != http.StatusForbidden {
				t.Errorf("local refusal: status=%d message=%s", status, message)
			}
			if got := fleet.took(); len(got) != 0 {
				t.Fatalf("forbidden decision reached upstream: %v", got)
			}
			restingUntil.Lock()
			rested := len(restingUntil.m)
			restingUntil.Unlock()
			if rested != 0 {
				t.Errorf("local policy refusal rested %d healthy accounts", rested)
			}
			for _, route := range s.Trace(t.Context(), 0, 0).Routes {
				for _, attempt := range route.Tries {
					if attempt.Rest != nil || attempt.Status != http.StatusForbidden {
						t.Errorf("local refusal recorded as upstream failure: %+v", attempt)
					}
				}
			}
			allowOnly(t, "jev-latest")
			if status, message := ask(); status != http.StatusOK {
				t.Fatalf("allowed decision did not recover: %d %s", status, message)
			}
			if got := fleet.took(); len(got) != 1 {
				t.Fatalf("allowed decision asked %v, want one healthy account", got)
			}
		})
	}
}

func TestModelPolicyDecisionGroupSkipsForbidden(t *testing.T) {
	for _, surface := range []string{"systemone", "classifier"} {
		t.Run(surface, func(t *testing.T) {
			fresh(t)
			var fleet = &decideFleet{status: map[string]int{}}
			up := fleet.serve()
			t.Cleanup(up.Close)
			for _, id := range []string{"blocked", "allowed"} {
				if err := provider.Save(provider.Provider{ID: id, Name: id, Key: id, Decide: up.URL + "/v1"}); err != nil {
					t.Fatal(err)
				}
			}
			if err := provider.SaveGroup(provider.Group{ID: "policy", Name: "Policy", Routing: provider.Ordered, Members: []string{"blocked/jev-blocked", "allowed/jev-allowed"}}); err != nil {
				t.Fatal(err)
			}
			allowOnly(t, "jev-allowed")
			s := New()
			if surface == "classifier" {
				if _, err := s.askClassifier("group/policy", []string{"bug"}, before{}, false, "fix this"); err != nil {
					t.Fatal(err)
				}
			} else {
				w := httptest.NewRecorder()
				r := httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(`{"model":"group/policy","state":{"message":"hi"},"questions":{}}`))
				s.Handler().ServeHTTP(w, r)
				if w.Code != http.StatusOK {
					t.Fatalf("allowed group member: %d %s", w.Code, w.Body.String())
				}
			}
			if got := fleet.took(); len(got) != 1 || got[0] != "allowed" {
				t.Fatalf("decision group asked %v, want only its allowed model", got)
			}
			restingUntil.Lock()
			rested := len(restingUntil.m)
			restingUntil.Unlock()
			if rested != 0 {
				t.Fatalf("skipping a forbidden member rested %d accounts", rested)
			}
		})
	}
}
