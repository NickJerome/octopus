package requesttransform

import (
	"context"
	"fmt"
	"slices"
	"strconv"
)

// applyPrompt visits only protocol-defined instruction text, preserving all other content and metadata.
func applyPrompt(ctx context.Context, doc map[string]any, protocol string, action Action, trace *Trace, growthBudget *int) error {
	rewrite := func(text, path string) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		trace.Matched++
		next, count, err := replaceText(text, action, min(MaxBodyBytes, len(text)+*growthBudget))
		if err != nil {
			return "", err
		}
		trace.Replacements += count
		*growthBudget -= max(0, len(next)-len(text))
		if next != text {
			trace.Paths = append(trace.Paths, path)
		}
		return next, nil
	}
	// Empty text blocks are removed only when this action made them empty.
	content := func(value any, path string) (any, bool, error) {
		switch value := value.(type) {
		case string:
			next, err := rewrite(value, path)
			return next, value != "" && next == "", err
		case []any:
			result := make([]any, 0, len(value))
			removed := false
			for i, part := range value {
				block, ok := part.(map[string]any)
				if !ok || (block["type"] != "text" && !(protocol == Responses && block["type"] == "input_text")) {
					result = append(result, part)
					continue
				}
				text, ok := block["text"].(string)
				if !ok {
					return nil, false, fmt.Errorf("%s/%d/text must be a string", path, i)
				}
				next, err := rewrite(text, path+"/"+strconv.Itoa(i)+"/text")
				if err != nil {
					return nil, false, err
				}
				if next == "" && text != "" {
					removed = true
					if _, ok := block["cache_control"]; ok {
						trace.Warnings = append(trace.Warnings, "removed empty text block with cache_control at "+path+"/"+strconv.Itoa(i))
					}
					continue
				}
				block["text"] = next
				result = append(result, block)
			}
			return result, removed && len(result) == 0, nil
		default:
			return nil, false, fmt.Errorf("%s must be text or content blocks", path)
		}
	}
	if slices.Contains(action.Target.Roles, "system") {
		field := ""
		if protocol == Responses {
			field = "instructions"
		}
		if protocol == Anthropic {
			field = "system"
		}
		if value, exists := doc[field]; field != "" && exists && value != nil {
			next, empty, err := content(value, "/"+field)
			if err != nil {
				return err
			}
			if empty {
				delete(doc, field)
			} else {
				doc[field] = next
			}
		}
	}
	field := "messages"
	if protocol == Anthropic {
		return nil
	}
	if protocol == Responses {
		field = "input"
	}
	items, ok := doc[field].([]any)
	if !ok {
		return nil
	} // Responses also accepts a user input string.
	result := make([]any, 0, len(items))
	for i, item := range items {
		message, ok := item.(map[string]any)
		if !ok {
			result = append(result, item)
			continue
		}
		role, _ := message["role"].(string)
		kind, _ := message["type"].(string)
		if !slices.Contains(action.Target.Roles, role) || (protocol == Responses && kind != "" && kind != "message") {
			result = append(result, item)
			continue
		}
		value, exists := message["content"]
		if !exists || value == nil {
			result = append(result, item)
			continue
		}
		next, empty, err := content(value, "/"+field+"/"+strconv.Itoa(i)+"/content")
		if err != nil {
			return err
		}
		if empty {
			continue
		}
		message["content"] = next
		result = append(result, message)
	}
	doc[field] = result
	return nil
}
