package main

import "testing"

func testEntry() CatalogEntry {
	min, max := 0, 100
	return CatalogEntry{
		ID: "demo",
		Params: []ParamSpec{
			{Name: "prune_gb", Type: "int", Default: float64(0), Min: &min, Max: &max},
			{Name: "label", Type: "string"},
			{Name: "mode", Type: "enum", Enum: []string{"a", "b"}},
		},
	}
}

func TestValidateParamsAppliesDefault(t *testing.T) {
	out, err := ValidateParams(testEntry(), map[string]any{"label": "x", "mode": "a"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["prune_gb"] != 0 {
		t.Errorf("prune_gb = %v, expected 0", out["prune_gb"])
	}
}

// Strict contract: a declared parameter without a default value is required.
// Renderers may index the map without existence checks (user ruling 2026-08-20).
func TestValidateParamsRejectsMissingRequired(t *testing.T) {
	if _, err := ValidateParams(testEntry(), map[string]any{"mode": "a"}); err == nil {
		t.Fatal("expected error for missing required parameter label")
	}
}

func TestValidateParamsRejectsOutOfRange(t *testing.T) {
	if _, err := ValidateParams(testEntry(), map[string]any{"prune_gb": float64(9999)}); err == nil {
		t.Fatal("expected error for prune_gb=9999")
	}
}

func TestValidateParamsRejectsUnknownParam(t *testing.T) {
	if _, err := ValidateParams(testEntry(), map[string]any{"nope": float64(1)}); err == nil {
		t.Fatal("expected error for unknown parameter")
	}
}

func TestValidateParamsRejectsEnumMiss(t *testing.T) {
	if _, err := ValidateParams(testEntry(), map[string]any{"mode": "c"}); err == nil {
		t.Fatal("expected error for mode=c")
	}
}

// The core: a newline in a string parameter would open a new directive in a
// line-based config file.
func TestValidateParamsRejectsControlChars(t *testing.T) {
	bad := []string{
		"ok\nrpcallowip=0.0.0.0/0",
		"ok\r\nlisten=1",
		"ok\x00rest",
	}
	for _, v := range bad {
		if _, err := ValidateParams(testEntry(), map[string]any{"label": v}); err == nil {
			t.Errorf("expected error for %q", v)
		}
	}
}

// Hardening: reject float64 values outside the safe integer range (±2^53).
// Beyond 2^53, float64 integers are gappy and JSON cannot transport them losslessly.
// This catches values that would silently convert to int with loss of precision or
// overflow behavior.
func TestValidateParamsRejectsUnsafeIntegerRange(t *testing.T) {
	// Create entry with int parameter that has NO Min/Max constraints
	entry := CatalogEntry{
		ID: "test-unsafe-range",
		Params: []ParamSpec{
			{Name: "big_int", Type: "int", Default: float64(0)},
		},
	}

	testCases := []struct {
		name  string
		value float64
	}{
		{"1e20 too large", 1e20},
		{"-1e20 too small", -1e20},
		{"2^63 exact", 9223372036854775808.0},
		{"2^53+2 loses precision", float64(9007199254740992 + 2)},
	}

	for _, tc := range testCases {
		if _, err := ValidateParams(entry, map[string]any{"big_int": tc.value}); err == nil {
			t.Errorf("expected error for %s (value=%v)", tc.name, tc.value)
		}
	}
}

// Verify that normal values still pass.
func TestValidateParamsAcceptsNormalIntegers(t *testing.T) {
	entry := CatalogEntry{
		ID: "test-normal-range",
		Params: []ParamSpec{
			{Name: "normal_int", Type: "int", Default: float64(0)},
		},
	}
	if _, err := ValidateParams(entry, map[string]any{"normal_int": float64(42)}); err != nil {
		t.Errorf("expected no error for normal value 42, got %v", err)
	}
}

func TestValidateParamsPatternMatch(t *testing.T) {
	entry := CatalogEntry{
		ID: "test-pattern",
		Params: []ParamSpec{
			{Name: "sig", Type: "string", Pattern: "^[a-z]{1,8}$"},
		},
	}
	if _, err := ValidateParams(entry, map[string]any{"sig": "abc"}); err != nil {
		t.Errorf("expected no error for 'abc', got %v", err)
	}
}

func TestValidateParamsPatternRejectsUppercase(t *testing.T) {
	entry := CatalogEntry{
		ID: "test-pattern",
		Params: []ParamSpec{
			{Name: "sig", Type: "string", Pattern: "^[a-z]{1,8}$"},
		},
	}
	if _, err := ValidateParams(entry, map[string]any{"sig": "ABC"}); err == nil {
		t.Error("expected error for 'ABC'")
	}
}

func TestValidateParamsPatternRejectsTooLong(t *testing.T) {
	entry := CatalogEntry{
		ID: "test-pattern",
		Params: []ParamSpec{
			{Name: "sig", Type: "string", Pattern: "^[a-z]{1,8}$"},
		},
	}
	if _, err := ValidateParams(entry, map[string]any{"sig": "toolonghere"}); err == nil {
		t.Error("expected error for 'toolonghere'")
	}
}

func TestValidateParamsInvalidPattern(t *testing.T) {
	entry := CatalogEntry{
		ID: "test-pattern",
		Params: []ParamSpec{
			{Name: "sig", Type: "string", Pattern: "(["},
		},
	}
	if _, err := ValidateParams(entry, map[string]any{"sig": "abc"}); err == nil {
		t.Error("expected error for invalid pattern")
	}
}

func TestValidateParamsPatternControlCharsFirst(t *testing.T) {
	entry := CatalogEntry{
		ID: "test-pattern",
		Params: []ParamSpec{
			{Name: "sig", Type: "string", Pattern: "^[a-z]{1,8}$"},
		},
	}
	// "a\nb" contains a control character, so it must fail even though it
	// would not match the pattern anyway — control char check runs first.
	if _, err := ValidateParams(entry, map[string]any{"sig": "a\nb"}); err == nil {
		t.Error("expected error for control char in pattern string")
	}
}
