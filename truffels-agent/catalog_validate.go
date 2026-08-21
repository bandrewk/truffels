package main

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// catalogIDRe matches valid catalog entry IDs: lowercase letters, digits, hyphens,
// 1-32 characters long, must start with letter or digit.
var catalogIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// patternCache compiles each ParamSpec pattern once and reuses it. ValidateParams
// runs on every install and admission check; recompiling the same fixed pattern
// each time was pure waste.
var patternCache sync.Map // string -> *regexp.Regexp

func compilePattern(pattern string) (*regexp.Regexp, error) {
	if v, ok := patternCache.Load(pattern); ok {
		return v.(*regexp.Regexp), nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	patternCache.Store(pattern, re)
	return re, nil
}

// isValidCatalogID checks if the ID is a valid catalog entry identifier.
func isValidCatalogID(s string) bool { return catalogIDRe.MatchString(s) }

// rejectsControlChars rejects newlines and control characters. Reason: values also
// end up in line-based formats (.conf, .env, Caddyfile), where a "\n" opens a new
// directive. The compose generator is protected by json.Marshal — this check is for
// everything else.
func rejectsControlChars(s string) error {
	for i, r := range s {
		if r == '\n' || r == '\r' || r < 0x20 || r == 0x7f {
			return fmt.Errorf("control character at position %d not allowed", i)
		}
	}
	return nil
}

// ValidateParams validates and coerces user-provided parameters against catalog
// entry specifications. It applies defaults, checks types, ranges, and enum values,
// and rejects control characters in string values.
func ValidateParams(e CatalogEntry, in map[string]any) (map[string]any, error) {
	specs := make(map[string]ParamSpec, len(e.Params))
	for _, p := range e.Params {
		specs[p.Name] = p
	}
	for name := range in {
		if _, ok := specs[name]; !ok {
			return nil, fmt.Errorf("unknown parameter %q", name)
		}
	}

	out := make(map[string]any, len(specs))
	for name, spec := range specs {
		raw, given := in[name]
		if !given {
			if spec.Default == nil {
				return nil, fmt.Errorf("parameter %q is required and has no default", name)
			}
			raw = spec.Default
		}
		v, err := coerceParam(spec, raw)
		if err != nil {
			return nil, fmt.Errorf("parameter %q: %w", name, err)
		}
		out[name] = v
	}
	return out, nil
}

// coerceParam validates and converts a raw parameter value according to its
// specification. It handles type checking, range validation, enum validation,
// and control character checks for strings.
func coerceParam(spec ParamSpec, raw any) (any, error) {
	switch spec.Type {
	case "int":
		f, ok := raw.(float64) // encoding/json delivers numbers as float64
		if !ok {
			return nil, fmt.Errorf("expected number, got %T", raw)
		}
		// Reject values outside ±2^53 (9.007199254740992e15) before any int(f) conversion.
		// This is the safe integer range where all values can be represented exactly in float64
		// and transported losslessly through JSON. Beyond ±2^53, float64 integers are gappy and
		// JSON cannot preserve them without loss. Also prevents int(f) saturation on extreme values.
		const safeIntMax = 9007199254740992.0  // 2^53
		const safeIntMin = -9007199254740992.0 // -2^53
		if f > safeIntMax || f < safeIntMin {
			return nil, fmt.Errorf("number out of safe integer range")
		}
		if f != float64(int(f)) {
			return nil, fmt.Errorf("expected integer, got %v", f)
		}
		n := int(f)
		if spec.Min != nil && n < *spec.Min {
			return nil, fmt.Errorf("%d is below minimum %d", n, *spec.Min)
		}
		if spec.Max != nil && n > *spec.Max {
			return nil, fmt.Errorf("%d exceeds maximum %d", n, *spec.Max)
		}
		return n, nil
	case "bool":
		b, ok := raw.(bool)
		if !ok {
			return nil, fmt.Errorf("expected bool, got %T", raw)
		}
		return b, nil
	case "string":
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("expected string, got %T", raw)
		}
		if err := rejectsControlChars(s); err != nil {
			return nil, err
		}
		if spec.Pattern != "" {
			// Patterns are anchored by the catalog author, not implicitly —
			// a catalog author writes ^…$ themselves; the code does not enforce it
			// (documented decision so partial matches remain possible where intended).
			re, err := compilePattern(spec.Pattern)
			if err != nil {
				return nil, fmt.Errorf("invalid pattern %q: %w", spec.Pattern, err)
			}
			if !re.MatchString(s) {
				return nil, fmt.Errorf("value %q does not match pattern %q", s, spec.Pattern)
			}
		}
		return s, nil
	case "enum":
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("expected string, got %T", raw)
		}
		for _, allowed := range spec.Enum {
			if s == allowed {
				return s, nil
			}
		}
		return nil, fmt.Errorf("%q not in [%s]", s, strings.Join(spec.Enum, ", "))
	default:
		return nil, fmt.Errorf("unknown type %q", spec.Type)
	}
}
