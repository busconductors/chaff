package shuffle

import (
	"strings"
	"testing"
)

func TestShuffleBasic(t *testing.T) {
	// Input: Stage 1 output with \ddd-encoded string.
	input := `print("\104\101\108\108\111")`
	output, err := Shuffle(input, 42)
	if err != nil {
		t.Fatalf("Shuffle: %v", err)
	}
	// Should contain z4() call.
	if !strings.Contains(output, "z4(") {
		t.Error("expected z4() call in output")
	}
	// Should NOT contain the raw \ddd string (it's now inside z4).
	if strings.Contains(output, `"\104\101\108\108\111"`) {
		t.Error("raw encoded string should be replaced with z4() call")
	}
}

func TestShufflePreservesNonStrings(t *testing.T) {
	input := `local x = 42; print("\104\105")`
	output, err := Shuffle(input, 42)
	if err != nil {
		t.Fatalf("Shuffle: %v", err)
	}
	if !strings.Contains(output, "local x = 42") {
		t.Error("non-string code should be preserved")
	}
}

func TestShuffleDeterminism(t *testing.T) {
	input := `print("\097\098\099")`
	a, _ := Shuffle(input, 42)
	b, _ := Shuffle(input, 42)
	if a != b {
		t.Error("same seed should produce identical output")
	}
}

func TestShuffleNoStrings(t *testing.T) {
	input := `local x = 42; return x`
	output, err := Shuffle(input, 42)
	if err != nil {
		t.Fatalf("Shuffle: %v", err)
	}
	if output != input {
		t.Error("input with no strings should pass through unchanged")
	}
}

func TestShuffleEmptyString(t *testing.T) {
	input := `local s = ""`
	output, err := Shuffle(input, 42)
	if err != nil {
		t.Fatalf("Shuffle: %v", err)
	}
	// Empty strings should be left alone.
	if !strings.Contains(output, `""`) {
		t.Error("empty string should pass through")
	}
}

func TestShuffleSingleByteString(t *testing.T) {
	input := `print("\065")` // single byte "A"
	output, err := Shuffle(input, 42)
	if err != nil {
		t.Fatalf("Shuffle: %v", err)
	}
	// Single byte: z4 still works with 1 chunk.
	if !strings.Contains(output, "z4(") {
		t.Error("single-byte string should still be shuffled")
	}
}
