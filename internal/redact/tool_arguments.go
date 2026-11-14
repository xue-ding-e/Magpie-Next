package redact

import (
	"encoding/json"
	"strconv"

	"github.com/yetone/magpie/internal/lazyre"
)

// argumentRules names serialized JSON arguments at their protocol positions.
// Its fields are the parameters that the tool declaration says are encrypted;
// they are opaque only on a function call that the backend sealed itself.
type argumentRules map[string]map[string]bool

var opaqueToolArgument = lazyre.New(`^gAAAAA[A-Za-z0-9_-]+={0,2}$`)

func functionArguments(v any) argumentRules {
	request, _ := v.(map[string]any)
	if request == nil {
		return nil
	}
	var encrypted map[string]map[string]bool
	encrypted = make(map[string]map[string]bool)
	var declarations func([]any, string)
	declarations = func(tools []any, namespace string) {
		for _, raw := range tools {
			tool, _ := raw.(map[string]any)
			if tool == nil {
				continue
			}
			if tool["type"] == "namespace" {
				name, _ := tool["name"].(string)
				children, _ := tool["tools"].([]any)
				declarations(children, name)
				continue
			}
			if function, ok := tool["function"].(map[string]any); ok {
				tool = function
			}
			name, _ := tool["name"].(string)
			parameters, _ := tool["parameters"].(map[string]any)
			properties, _ := parameters["properties"].(map[string]any)
			for field, raw := range properties {
				property, _ := raw.(map[string]any)
				if property["encrypted"] != true {
					continue
				}
				key := namespace + "/" + name
				if encrypted[key] == nil {
					encrypted[key] = make(map[string]bool)
				}
				encrypted[key][field] = true
			}
		}
	}
	for _, key := range []string{"tools", "functions"} {
		tools, _ := request[key].([]any)
		declarations(tools, "")
	}
	items, _ := request["input"].([]any)
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		kind, _ := item["type"].(string)
		if defItems[kind] {
			tools, _ := item["tools"].([]any)
			declarations(tools, "")
		}
	}
	var rules argumentRules
	rules = make(argumentRules)
	add := func(path string, call map[string]any, sealed bool) {
		if _, ok := call["arguments"].(string); !ok {
			return
		}
		var fields map[string]bool
		if content, _ := call["encrypted_content"].(string); sealed && opaqueToolArgument.MatchString(content) {
			name, _ := call["name"].(string)
			namespace, _ := call["namespace"].(string)
			fields = encrypted[namespace+"/"+name]
		}
		rules[join(path, "arguments")] = fields
	}
	for i, raw := range items {
		item, _ := raw.(map[string]any)
		if item["type"] == "function_call" {
			add(join("input", strconv.Itoa(i)), item, true)
		}
	}
	messages, _ := request["messages"].([]any)
	for i, raw := range messages {
		message, _ := raw.(map[string]any)
		path := join("messages", strconv.Itoa(i))
		if call, ok := message["function_call"].(map[string]any); ok {
			add(join(path, "function_call"), call, false)
		}
		calls, _ := message["tool_calls"].([]any)
		for j, raw := range calls {
			call, _ := raw.(map[string]any)
			if function, ok := call["function"].(map[string]any); ok {
				add(join(join(join(path, "tool_calls"), strconv.Itoa(j)), "function"), function, false)
			}
		}
	}
	return rules
}

// walkArguments lets masking see an argument's string values rather than its
// JSON syntax. A model span can consume an entire value without consuming the
// surrounding quotes, escapes or delimiters. Non-JSON arguments retain the
// existing text handling; a field merely named arguments is not special.
// bare handles primitive JSON values through the caller's existing JSON rules,
// including numeric literals the local dictionary masks or partly allows.
func walkArguments(body []byte, rules argumentRules, fn func(path, key, s string) string, bare func(path, key, raw string) string) ([]byte, bool) {
	return walk(body, func(path, key, s string) string {
		fields, ok := rules[path]
		if !ok || !json.Valid([]byte(s)) {
			return fn(path, key, s)
		}
		var primitive func(child, name, raw string) string
		if bare != nil {
			primitive = func(child, name, raw string) string {
				return bare(join(path, child), name, raw)
			}
		}
		out, ok := walkWithBare([]byte(s), func(child, name, value string) string {
			if child == name && fields[name] && opaqueToolArgument.MatchString(value) {
				return value
			}
			return fn(join(path, child), name, value)
		}, primitive)
		if !ok {
			return fn(path, key, s)
		}
		return string(out)
	})
}
