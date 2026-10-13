package requesttransform

import (
	"fmt"
	"strconv"
	"strings"
)

func parsePath(path string) ([]string, error) {
	if !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("path %q must be a non-root JSON Pointer starting with /", path)
	}
	parts := strings.Split(path[1:], "/")
	if len(parts) > 128 {
		return nil, fmt.Errorf("path is too deep")
	}
	for i, part := range parts {
		for j := 0; j < len(part); j++ {
			if part[j] == '~' {
				j++
				if j == len(part) || (part[j] != '0' && part[j] != '1') {
					return nil, fmt.Errorf("invalid JSON Pointer escape in %q", path)
				}
			}
		}
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
	}
	return parts, nil
}

func index(part string, length int) (int, error) {
	i, err := strconv.Atoi(part)
	if err != nil || i < 0 || strconv.Itoa(i) != part {
		return 0, fmt.Errorf("invalid array index %q", part)
	}
	if i >= length {
		return 0, fmt.Errorf("array index %d is out of bounds", i)
	}
	return i, nil
}

func lookup(node any, path []string) (any, bool, error) {
	for _, part := range path {
		switch current := node.(type) {
		case map[string]any:
			var ok bool
			node, ok = current[part]
			if !ok {
				return nil, false, nil
			}
		case []any:
			if i, err := strconv.Atoi(part); err == nil && i >= len(current) && strconv.Itoa(i) == part {
				return nil, false, nil
			}
			i, err := index(part, len(current))
			if err != nil {
				return nil, false, err
			}
			node = current[i]
		default:
			return nil, false, fmt.Errorf("cannot traverse a non-container at %q", part)
		}
	}
	return node, true, nil
}

func setAt(node any, path []string, value any, create bool) (any, error) {
	if len(path) == 0 {
		return value, nil
	}
	part := path[0]
	switch current := node.(type) {
	case map[string]any:
		child, ok := current[part]
		if !ok && len(path) > 1 {
			if !create {
				return nil, fmt.Errorf("missing parent %q (set create_missing to create objects)", part)
			}
			child = make(map[string]any)
		}
		next, err := setAt(child, path[1:], value, create)
		if err != nil {
			return nil, err
		}
		current[part] = next
		return current, nil
	case []any:
		i, err := index(part, len(current))
		if err != nil {
			return nil, err
		}
		next, err := setAt(current[i], path[1:], value, create)
		if err != nil {
			return nil, err
		}
		current[i] = next
		return current, nil
	default:
		return nil, fmt.Errorf("cannot traverse a non-container at %q", part)
	}
}

func removeAt(node any, path []string) (any, error) {
	part := path[0]
	switch current := node.(type) {
	case map[string]any:
		child, ok := current[part]
		if !ok {
			return current, nil
		}
		if len(path) == 1 {
			delete(current, part)
			return current, nil
		}
		next, err := removeAt(child, path[1:])
		if err != nil {
			return nil, err
		}
		current[part] = next
		return current, nil
	case []any:
		i, err := index(part, len(current))
		if err != nil {
			return nil, err
		}
		if len(path) == 1 {
			return append(current[:i], current[i+1:]...), nil
		}
		next, err := removeAt(current[i], path[1:])
		if err != nil {
			return nil, err
		}
		current[i] = next
		return current, nil
	default:
		return nil, fmt.Errorf("cannot traverse a non-container at %q", part)
	}
}

func mergeObjects(target, value map[string]any) map[string]any {
	result := make(map[string]any, len(target)+len(value))
	for key, v := range target {
		result[key] = v
	}
	for key, v := range value {
		left, leftOK := target[key].(map[string]any)
		right, rightOK := v.(map[string]any)
		if leftOK && rightOK {
			result[key] = mergeObjects(left, right)
		} else {
			result[key] = v
		}
	}
	return result
}
