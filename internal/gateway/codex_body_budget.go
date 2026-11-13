package gateway

import "strings"

// ChatGPT's Codex backend currently rejects sufficiently large Responses
// bodies with only {"detail":"Bad Request"}. The limit is below the model
// context limit, so a token-only compaction trigger can arrive too late.
// Leave headroom below the observed upstream cliff for headers and future
// wire fields; this is a preflight for Codex's own ChatGPT route only.
const codexResponsesBodyBudget = 800 << 10

func codexBodyNeedsCompaction(body []byte) bool {
	return len(responsesWireBody(body)) > codexResponsesBodyBudget
}

func codexBudgetedCompactBody(body []byte) []byte {
	body = responsesWireBody(body)
	plain := plainCompact(body)
	if len(plain) <= codexResponsesBodyBudget {
		return plain
	}
	// plainCompact already caps conversation text. If a request's tool
	// definitions are unusually large too, remove only definitions from this
	// summary request; the original history remains in the client's session.
	q, err := rawObject(plain)
	if err != nil {
		return plain
	}
	delete(q, "tools")
	delete(q, "tool_choice")
	trimmed, err := marshalPlain(q)
	if err != nil || len(trimmed) > codexResponsesBodyBudget {
		return plain
	}
	return trimmed
}

func isCodexOwnResponsesTurn(rest, model string, body []byte) bool {
	return rest == "/responses" && model != "" && !strings.Contains(model, "/") && !compactionTriggerIn(body)
}
