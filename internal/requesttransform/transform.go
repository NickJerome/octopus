package requesttransform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/tidwall/sjson"
)

type Trace struct {
	RuleID       string   `json:"rule_id"`
	Action       int      `json:"action"` // One-based; zero denotes a skipped rule.
	Op           string   `json:"op,omitempty"`
	Status       string   `json:"status"`
	Matched      int      `json:"matched"`
	Replacements int      `json:"replacements"`
	Paths        []string `json:"paths"`
	Warnings     []string `json:"warnings"`
}

func ValidateOverride(raw string) error {
	_, err := legacyOverrides(raw)
	return err
}

func legacyOverrides(raw string) (map[string]json.RawMessage, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, failure("param_override must be a JSON object: %v", err)
	}
	return values, nil
}

// Rewrite is shared by the relay and preview. Input bytes are never modified, including on failure.
func Rewrite(ctx context.Context, overrides string, config []byte, body []byte, meta Context) ([]byte, []Trace, error) {
	program, err := Compile(config)
	if err != nil {
		return nil, nil, err
	}
	values, err := legacyOverrides(overrides)
	if err != nil {
		return nil, nil, err
	}
	escape := strings.NewReplacer("\\", "\\\\", ".", "\\.", ":", "\\:")
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		if key == "model" || key == "stream" {
			continue
		}
		body, err = sjson.SetRawBytes(body, ":"+escape.Replace(key), values[key])
		if err != nil {
			return nil, nil, failure("param_override %q: %v", key, err)
		}
	}
	body, trace, err := program.Apply(ctx, body, meta)
	if err != nil {
		return nil, trace, err
	}
	// A configured rule set cannot disable usage reporting required by the relay.
	if program != nil && meta.TargetProtocol == Chat && meta.Stream {
		body, err = sjson.SetBytes(body, "stream_options.include_usage", true)
		if err != nil {
			return nil, trace, failure("stream usage: %v", err)
		}
	}
	return body, trace, nil
}

func (p *Program) Apply(ctx context.Context, body []byte, meta Context) ([]byte, []Trace, error) {
	trace := []Trace{}
	if p == nil || len(p.rules) == 0 {
		return body, trace, nil
	}
	if !ValidProtocol(meta.TargetProtocol) {
		return nil, trace, failure("unknown target protocol %q", meta.TargetProtocol)
	}
	if len(body) > MaxBodyBytes {
		return nil, trace, failure("request body exceeds %d bytes", MaxBodyBytes)
	}
	value, err := decode(body)
	if err != nil {
		return nil, trace, failure("request body: %v", err)
	}
	doc, ok := value.(map[string]any)
	if !ok || doc == nil {
		return nil, trace, failure("request body must be a JSON object")
	}
	modelBefore, streamBefore := doc["model"], doc["stream"]
	changed, work := false, 0
	growthBudget := MaxBodyBytes - len(body)
	for _, rule := range p.rules {
		if err := ctx.Err(); err != nil {
			return nil, trace, err
		}
		if rule.Enabled != nil && !*rule.Enabled {
			trace = append(trace, Trace{RuleID: rule.ID, Status: "skipped", Paths: []string{}, Warnings: []string{}})
			continue
		}
		matches, err := matchesRule(rule.When, meta, doc)
		if err != nil {
			return nil, trace, failure("rule %q condition: %v", rule.ID, err)
		}
		if !matches {
			trace = append(trace, Trace{RuleID: rule.ID, Status: "skipped", Paths: []string{}, Warnings: []string{}})
			continue
		}
		for i, action := range rule.Actions {
			if err := ctx.Err(); err != nil {
				return nil, trace, err
			}
			work += MaxBodyBytes - growthBudget
			if work > 512<<20 {
				return nil, trace, failure("rule execution exceeds byte processing budget")
			}
			entry := Trace{RuleID: rule.ID, Action: i + 1, Op: action.Op, Status: "unchanged", Paths: []string{}, Warnings: []string{}}
			if action.Target.Scope == "prompt_text" {
				err = applyPrompt(ctx, doc, meta.TargetProtocol, action, &entry, &growthBudget)
			} else {
				err = applyAction(doc, action, &entry, &growthBudget)
			}
			if err == nil && action.RequireMatch && entry.Matched == 0 {
				err = fmt.Errorf("required target did not match")
			}
			if err != nil {
				entry.Status = "failed"
				trace = append(trace, entry)
				return nil, trace, failure("rule %q action %d (%s %s): %v", rule.ID, i+1, action.Op, action.Target.Path, err)
			}
			if len(entry.Paths) > 0 {
				entry.Status = "modified"
				changed = true
			} else if entry.Matched == 0 {
				entry.Status = "skipped"
			}
			trace = append(trace, entry)
		}
	}
	if !changed {
		return body, trace, nil
	}
	if !reflect.DeepEqual(modelBefore, doc["model"]) || !reflect.DeepEqual(streamBefore, doc["stream"]) {
		return nil, trace, failure("model/stream must not change")
	}
	if messages, ok := doc["messages"].([]any); ok && len(messages) == 0 {
		return nil, trace, failure("modification left messages empty")
	}
	if err := checkDepth(doc, 0); err != nil {
		return nil, trace, failure("result: %v", err)
	}
	result, err := json.Marshal(doc)
	if err != nil {
		return nil, trace, failure("encode body: %v", err)
	}
	if len(result) > MaxBodyBytes {
		return nil, trace, failure("result exceeds %d bytes", MaxBodyBytes)
	}
	return result, trace, nil
}

func matchesRule(c Conditions, meta Context, doc map[string]any) (bool, error) {
	for _, pair := range []struct {
		values []string
		value  string
	}{
		{c.SourceProtocol, meta.SourceProtocol}, {c.TargetProtocol, meta.TargetProtocol},
		{c.SourceModel, meta.SourceModel}, {c.TargetModel, meta.TargetModel},
	} {
		if len(pair.values) > 0 && !slices.Contains(pair.values, pair.value) {
			return false, nil
		}
	}
	if c.Stream != nil && *c.Stream != meta.Stream {
		return false, nil
	}
	for _, path := range c.Exists {
		parts, _ := parsePath(path)
		_, exists, err := lookup(doc, parts)
		if err != nil {
			return false, err
		}
		if !exists {
			return false, nil
		}
	}
	for path, raw := range c.Equals {
		parts, _ := parsePath(path)
		actual, exists, err := lookup(doc, parts)
		if err != nil {
			return false, err
		}
		expected, err := decode(raw)
		if err != nil {
			return false, err
		}
		if !exists || !reflect.DeepEqual(actual, expected) {
			return false, nil
		}
	}
	return true, nil
}

func applyAction(doc map[string]any, a Action, trace *Trace, growthBudget *int) error {
	current, exists, err := lookup(doc, a.path)
	if err != nil {
		return err
	}
	if exists {
		trace.Matched = 1
	}
	if !exists && a.Op != "set" {
		return nil
	}
	if a.RequireMatch && !exists {
		return nil
	}
	if a.Op == "set" && a.OnlyIfMissing && exists {
		return nil
	}
	var next any
	switch a.Op {
	case "set":
		next, err = decode(a.Value)
		if !exists {
			trace.Matched = 1
		}
	case "remove":
		_, err = removeAt(doc, a.path)
		if err == nil {
			trace.Paths = append(trace.Paths, a.Target.Path)
		}
		return err
	case "merge":
		object, ok := current.(map[string]any)
		if !ok {
			return fmt.Errorf("merge target must be an object")
		}
		value, decodeErr := decode(a.Value)
		if decodeErr != nil {
			return decodeErr
		}
		next = mergeObjects(object, value.(map[string]any))
	default:
		text, ok := current.(string)
		if !ok {
			return fmt.Errorf("text target must be a string")
		}
		var count int
		next, count, err = replaceText(text, a, min(MaxBodyBytes, len(text)+*growthBudget))
		if err == nil {
			*growthBudget -= max(0, len(next.(string))-len(text))
		}
		trace.Replacements += count
	}
	if err != nil {
		return err
	}
	if exists && reflect.DeepEqual(current, next) {
		return nil
	}
	// Values in set/merge are bounded by configuration size; reserve their full encoded size conservatively.
	if a.Op == "set" || a.Op == "merge" {
		*growthBudget -= len(a.Value)
		if *growthBudget < 0 {
			return fmt.Errorf("result exceeds byte budget")
		}
	}
	_, err = setAt(doc, a.path, next, a.CreateMissing)
	if err == nil {
		trace.Paths = append(trace.Paths, a.Target.Path)
	}
	return err
}

func replaceText(text string, a Action, maxBytes int) (string, int, error) {
	limit := a.Limit
	if limit == 0 {
		limit = maxMatches + 1
	}
	var matches [][]int
	if a.regex != nil {
		matches = a.regex.FindAllStringSubmatchIndex(text, limit)
	} else {
		for offset := 0; len(matches) < limit; {
			pos := strings.Index(text[offset:], *a.Pattern)
			if pos < 0 {
				break
			}
			start := offset + pos
			offset = start + len(*a.Pattern)
			matches = append(matches, []int{start, offset})
		}
	}
	if len(matches) == 0 {
		return text, 0, nil
	}
	if len(matches) > maxMatches {
		return "", 0, fmt.Errorf("more than %d text matches", maxMatches)
	}
	var result bytes.Buffer
	offset := 0
	for _, match := range matches {
		replacement := *a.Replacement
		if a.ReplacementMode == "expand" {
			// Bound expansion before allocation, including repeated capture references.
			if int64(len(replacement))*int64(len(text)+1) > int64(maxBytes) {
				return "", 0, fmt.Errorf("capture expansion exceeds byte budget")
			}
			replacement = string(a.regex.ExpandString(nil, replacement, text, match))
		}
		if result.Len()+match[0]-offset+len(replacement) > maxBytes {
			return "", 0, fmt.Errorf("replacement exceeds byte budget")
		}
		result.WriteString(text[offset:match[0]])
		result.WriteString(replacement)
		offset = match[1]
	}
	if result.Len()+len(text)-offset > maxBytes {
		return "", 0, fmt.Errorf("replacement exceeds byte budget")
	}
	result.WriteString(text[offset:])
	return result.String(), len(matches), nil
}
