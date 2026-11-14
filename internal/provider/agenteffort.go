package provider

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/yetone/magpie/internal/settings"
)

// An agent can have an explicit gateway reasoning effort picked in magpie,
// kept by agent (settings' AgentEfforts). This also serves agents whose own
// config can't carry one (Cursor Private Inference, #1003: neither its
// environment nor its app picks one for most models). The gateway asks
// for it on requests to a model that reasons, in place of the level the
// agent sent: the user picked it. Cursor's own Reasoning control can be
// its default as often as a choice. None picked, requests go as asked.

// AgentEffort is the effort agent's requests are asked for, "" for none.
func AgentEffort(agent string) string {
	return heldSettings().AgentEfforts[strings.ToLower(agent)]
}

// SetAgentEffort asks agent's requests for level, one of MemberEfforts or
// Codex's Ultra mode; "" leaves each request's own level.
func SetAgentEffort(agent, level string) error {
	agent, level = strings.ToLower(strings.TrimSpace(agent)), strings.ToLower(strings.TrimSpace(level))
	if agent == "" {
		return errors.New("no agent")
	}
	levels := MemberEfforts
	if agent == "codex" {
		levels = append(slices.Clone(levels), "ultra")
	}
	if level != "" && !slices.Contains(levels, level) {
		return fmt.Errorf("%q is not a reasoning effort: one of %s", level, strings.Join(levels, ", "))
	}
	s := settings.Load()
	if s.AgentEfforts[agent] == level {
		return nil
	}
	if level == "" {
		delete(s.AgentEfforts, agent)
	} else {
		if s.AgentEfforts == nil {
			s.AgentEfforts = map[string]string{}
		}
		s.AgentEfforts[agent] = level
	}
	if err := settings.Save(s); err != nil {
		return err
	}
	Changed() // a held settings read sees it
	return nil
}
