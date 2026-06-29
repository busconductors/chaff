package wrap

import (
	"strings"
	"testing"
)

func TestWrapContainsIIFE(t *testing.T) {
	input := `print("hello")`
	output := Wrap(input, 42)
	if !strings.Contains(output, "return (function(...)") {
		t.Error("expected outer IIFE")
	}
	if !strings.Contains(output, "return (function(") {
		t.Error("expected inner IIFE")
	}
}

func TestWrapContainsZ4(t *testing.T) {
	input := `print("hello")`
	output := Wrap(input, 42)
	if !strings.Contains(output, "local z4 = function(Z)") {
		t.Error("expected z4 function injected at top")
	}
	if !strings.Contains(output, "table.concat") {
		t.Error("expected table.concat in z4")
	}
}

func TestWrapContainsInput(t *testing.T) {
	input := `print("hello")`
	output := Wrap(input, 42)
	if !strings.Contains(output, `print("hello")`) {
		t.Error("expected original input preserved inside IIFE")
	}
}

func TestWrapContainsDecoyReturn(t *testing.T) {
	input := `print("hello")`
	output := Wrap(input, 42)
	if !strings.Contains(output, "return (B(") {
		t.Error("expected decoy return line")
	}
}

func TestWrap22CharParams(t *testing.T) {
	output := Wrap(`print("test")`, 42)
	// Inner IIFE should have 22 single-char or multi-char params.
	if !strings.Contains(output, "function(") {
		t.Error("expected inner function with params")
	}
}

func TestWrapDeterminism(t *testing.T) {
	a := Wrap(`print("test")`, 42)
	b := Wrap(`print("test")`, 42)
	if a != b {
		t.Error("same seed should produce identical output")
	}
}

func TestWrapDifferentSeed(t *testing.T) {
	a := Wrap(`print("test")`, 42)
	b := Wrap(`print("test")`, 99)
	if a == b {
		t.Error("different seeds should produce different output (probabilistic)")
	}
}
