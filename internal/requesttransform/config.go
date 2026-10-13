// Package requesttransform applies ordered, declarative edits to an upstream JSON body.
// It has no dependency on routing, storage or a provider SDK.
package requesttransform

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"sync"
)

const (
	Chat           = "openai_chat"
	Responses      = "openai_responses"
	Anthropic      = "anthropic_messages"
	MaxConfigBytes = 64 << 10
	MaxBodyBytes   = 64 << 20
	maxActions     = 128
	maxMatches     = 10000
)

type Config struct {
	Version int    `json:"version"`
	Rules   []Rule `json:"rules"`
}

type Rule struct {
	ID      string     `json:"id"`
	Name    string     `json:"name,omitempty"`
	Enabled *bool      `json:"enabled,omitempty"`
	When    Conditions `json:"when,omitempty"`
	Actions []Action   `json:"actions"`
}

type Conditions struct {
	SourceProtocol []string                   `json:"source_protocol,omitempty"`
	TargetProtocol []string                   `json:"target_protocol,omitempty"`
	SourceModel    []string                   `json:"source_model,omitempty"`
	TargetModel    []string                   `json:"target_model,omitempty"`
	Stream         *bool                      `json:"stream,omitempty"`
	Exists         []string                   `json:"exists,omitempty"`
	Equals         map[string]json.RawMessage `json:"equals,omitempty"`
}

type Target struct {
	Path  string   `json:"path,omitempty"`
	Scope string   `json:"scope,omitempty"`
	Roles []string `json:"roles,omitempty"`
}

type Action struct {
	Op              string          `json:"op"`
	Target          Target          `json:"target"`
	Value           json.RawMessage `json:"value,omitempty"`
	CreateMissing   bool            `json:"create_missing,omitempty"`
	OnlyIfMissing   bool            `json:"only_if_missing,omitempty"`
	RequireMatch    bool            `json:"require_match,omitempty"`
	Pattern         *string         `json:"pattern,omitempty"`
	Replacement     *string         `json:"replacement,omitempty"`
	ReplacementMode string          `json:"replacement_mode,omitempty"`
	Limit           int             `json:"limit,omitempty"`
	path            []string
	regex           *regexp.Regexp
}

// Context is immutable routing metadata, never derived from a modified body.
type Context struct {
	SourceProtocol string `json:"source_protocol"`
	TargetProtocol string `json:"target_protocol"`
	SourceModel    string `json:"source_model"`
	TargetModel    string `json:"target_model"`
	Stream         bool   `json:"stream"`
}

// Error distinguishes deterministic configuration/execution failures from retryable upstream failures.
type Error struct{ Message string }

func (e *Error) Error() string                 { return "request transform: " + e.Message }
func failure(format string, args ...any) error { return &Error{Message: fmt.Sprintf(format, args...)} }

type Program struct{ rules []Rule }

var programs = struct {
	sync.Mutex
	items map[string]*Program
}{items: make(map[string]*Program)}

// Compile validates strictly and caches immutable programs. Cache keys contain only administrator configuration.
func Compile(raw []byte) (*Program, error) {
	if len(raw) > MaxConfigBytes {
		return nil, failure("configuration exceeds %d bytes", MaxConfigBytes)
	}
	key := string(bytes.TrimSpace(raw))
	if key == "" || key == "null" {
		return nil, nil
	}
	programs.Lock()
	program, ok := programs.items[key]
	programs.Unlock()
	if ok {
		return program, nil
	}
	var config Config
	decoder := json.NewDecoder(strings.NewReader(key))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return nil, failure("invalid configuration: %v", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, failure("configuration must contain one JSON object")
	}
	if config.Version != 1 {
		return nil, failure("unsupported version %d", config.Version)
	}
	if len(config.Rules) > 64 {
		return nil, failure("at most 64 rules are allowed")
	}
	ids, total := make(map[string]bool), 0
	for i := range config.Rules {
		rule := &config.Rules[i]
		if strings.TrimSpace(rule.ID) == "" || ids[rule.ID] {
			return nil, failure("rules[%d]: id must be non-empty and unique", i)
		}
		ids[rule.ID] = true
		for _, protocols := range [][]string{rule.When.SourceProtocol, rule.When.TargetProtocol} {
			for _, protocol := range protocols {
				if !ValidProtocol(protocol) {
					return nil, failure("rule %q: unknown protocol %q", rule.ID, protocol)
				}
			}
		}
		for _, path := range rule.When.Exists {
			if _, err := parsePath(path); err != nil {
				return nil, failure("rule %q: %v", rule.ID, err)
			}
		}
		for path, raw := range rule.When.Equals {
			if _, err := parsePath(path); err != nil {
				return nil, failure("rule %q: %v", rule.ID, err)
			}
			if _, err := decode(raw); err != nil {
				return nil, failure("rule %q equals %q: %v", rule.ID, path, err)
			}
		}
		if len(rule.Actions) == 0 {
			return nil, failure("rule %q: actions must not be empty", rule.ID)
		}
		total += len(rule.Actions)
		if total > maxActions {
			return nil, failure("at most %d actions are allowed", maxActions)
		}
		for j := range rule.Actions {
			if err := compileAction(&rule.Actions[j]); err != nil {
				return nil, failure("rule %q action %d: %v", rule.ID, j+1, err)
			}
		}
	}
	program = &Program{rules: config.Rules}
	programs.Lock()
	// Bounded generations avoid retaining unbounded editing/preview configurations.
	if len(programs.items) >= 128 {
		clear(programs.items)
	}
	programs.items[key] = program
	programs.Unlock()
	return program, nil
}

func ValidProtocol(protocol string) bool {
	return protocol == Chat || protocol == Responses || protocol == Anthropic
}

func compileAction(a *Action) error {
	text := a.Op == "replace" || a.Op == "regex_replace"
	if !text && a.Op != "set" && a.Op != "remove" && a.Op != "merge" {
		return fmt.Errorf("unknown operation %q", a.Op)
	}
	if a.Target.Scope != "" {
		if a.Target.Scope != "prompt_text" || a.Target.Path != "" || !text {
			return fmt.Errorf("prompt_text is only supported by text operations and cannot have a path")
		}
		if len(a.Target.Roles) == 0 {
			return fmt.Errorf("prompt_text requires roles")
		}
		for _, role := range a.Target.Roles {
			if !slices.Contains([]string{"system", "developer"}, role) {
				return fmt.Errorf("unsupported prompt role %q", role)
			}
		}
	} else {
		if len(a.Target.Roles) != 0 {
			return fmt.Errorf("roles require prompt_text scope")
		}
		var err error
		a.path, err = parsePath(a.Target.Path)
		if err != nil {
			return err
		}
		if a.path[0] == "model" || a.path[0] == "stream" || (a.path[0] == "stream_options" && (len(a.path) == 1 || a.path[1] == "include_usage")) {
			return fmt.Errorf("path %q is controlled by the relay", a.Target.Path)
		}
	}
	if a.Op == "set" || a.Op == "merge" {
		if len(a.Value) == 0 {
			return fmt.Errorf("%s requires value (null is an explicit value)", a.Op)
		}
		value, err := decode(a.Value)
		if err != nil {
			return err
		}
		if a.Op == "merge" {
			if _, ok := value.(map[string]any); !ok {
				return fmt.Errorf("merge value must be an object")
			}
		}
	} else if len(a.Value) != 0 {
		return fmt.Errorf("value is only supported by set/merge")
	}
	if (a.CreateMissing || a.OnlyIfMissing) && a.Op != "set" {
		return fmt.Errorf("create_missing/only_if_missing require set")
	}
	if text {
		if a.Pattern == nil || a.Replacement == nil {
			return fmt.Errorf("text operations require pattern and replacement")
		}
		if *a.Pattern == "" {
			return fmt.Errorf("pattern must not be empty")
		}
		if a.Limit < 0 || a.Limit > maxMatches {
			return fmt.Errorf("limit must be between 0 and %d", maxMatches)
		}
		if a.ReplacementMode != "" && a.ReplacementMode != "literal" && a.ReplacementMode != "expand" {
			return fmt.Errorf("unknown replacement_mode")
		}
		if a.Op == "replace" && a.ReplacementMode == "expand" {
			return fmt.Errorf("expand requires regex_replace")
		}
		if a.Op == "regex_replace" {
			var err error
			a.regex, err = regexp.Compile(*a.Pattern)
			if err != nil {
				return fmt.Errorf("invalid regular expression: %w", err)
			}
		}
	} else if a.Pattern != nil || a.Replacement != nil || a.ReplacementMode != "" || a.Limit != 0 {
		return fmt.Errorf("text options require replace/regex_replace")
	}
	return nil
}

func decode(raw []byte) (any, error) {
	if !json.Valid(raw) {
		return nil, fmt.Errorf("invalid JSON")
	}
	var value any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&value); err != nil {
		return nil, err
	}
	if err := checkDepth(value, 0); err != nil {
		return nil, err
	}
	return value, nil
}

func checkDepth(value any, depth int) error {
	if depth > 128 {
		return fmt.Errorf("JSON nesting exceeds 128 levels")
	}
	switch value := value.(type) {
	case map[string]any:
		for _, child := range value {
			if err := checkDepth(child, depth+1); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range value {
			if err := checkDepth(child, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}
