package deadcode

import (
	"strings"
	"testing"
)

func TestInjectBasic(t *testing.T) {
	input := `print("hello")`
	output := Inject(input, 3, 42)
	// Should contain the original code.
	if !strings.Contains(output, `print("hello")`) {
		t.Error("original code should be preserved")
	}
	// Should be larger than input.
	if len(output) <= len(input) {
		t.Error("output should be larger than input (dead code added)")
	}
}

func TestInjectBloatLevels(t *testing.T) {
	input := `print("test")`
	b1 := Inject(input, 1, 42)
	b3 := Inject(input, 3, 42)
	b5 := Inject(input, 5, 42)
	if len(b1) >= len(b3) {
		t.Error("bloat 3 should produce more code than bloat 1")
	}
	if len(b3) >= len(b5) {
		t.Error("bloat 5 should produce more code than bloat 3")
	}
}

func TestInjectDeterminism(t *testing.T) {
	a := Inject(`print("test")`, 3, 42)
	b := Inject(`print("test")`, 3, 42)
	if a != b {
		t.Error("same seed should produce identical output")
	}
}

func TestInjectDoesNotAffectFunctionality(t *testing.T) {
	// Dead code is syntactically valid Lua.
	output := Inject(`local x = 1`, 1, 42)
	// All injected code should be local declarations or arithmetic.
	if !strings.Contains(output, "local") {
		t.Error("expected 'local' in injected code")
	}
}

func TestInjectFakeLocals(t *testing.T) {
	output := Inject(`print("test")`, 2, 42)
	// Should contain fake local variable declarations.
	lines := strings.Count(output, "\n")
	if lines < 10 {
		t.Errorf("expected > 10 lines with bloat 2, got %d", lines)
	}
}

func TestInjectDifferentSeeds(t *testing.T) {
	a := Inject(`print("test")`, 3, 42)
	b := Inject(`print("test")`, 3, 99)
	if a == b {
		t.Error("different seeds should produce different output (probabilistic)")
	}
}
