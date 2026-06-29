package encrypt

import (
	"strings"
	"testing"
)

func TestEncryptBasicString(t *testing.T) {
	input := `print("hello")`
	output, err := Encrypt(input)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	// Should contain \ddd sequences, not the plain string.
	if strings.Contains(output, `"hello"`) {
		t.Error("plain string 'hello' should be encoded, not preserved")
	}
	if !strings.Contains(output, `\104`) {
		t.Error("expected decimal-encoded 'h' (\\104)")
	}
}

func TestEncryptEmptyString(t *testing.T) {
	input := `local s = ""`
	output, err := Encrypt(input)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if !strings.Contains(output, `""`) {
		t.Error("empty string should remain empty quotes")
	}
}

func TestEncryptEscapeSequences(t *testing.T) {
	input := `local s = "a\nb"`
	output, err := Encrypt(input)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	// \n is 0x0A = 010
	if !strings.Contains(output, `\010`) {
		t.Errorf("expected \\010 for newline byte, got: %s", output)
	}
}

func TestEncryptMultilineString(t *testing.T) {
	input := "local s = [[hello\nworld]]"
	output, err := Encrypt(input)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	// Multiline content should be encoded.
	if strings.Contains(output, "hello") {
		t.Error("long string content should be encoded")
	}
}

func TestEncryptAlreadyEncoded(t *testing.T) {
	input := `local s = "\104\101\108\108\111"`
	output, err := Encrypt(input)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	// Already-encoded strings should pass through unchanged.
	if !strings.Contains(output, `\104\101\108\108\111`) {
		t.Error("already-encoded string should pass through")
	}
}

func TestEncryptConcatStrings(t *testing.T) {
	input := `local s = "he" .. "llo"`
	output, err := Encrypt(input)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	// Both parts should be encoded separately.
	if strings.Contains(output, `"he"`) {
		t.Error("concat part should be encoded")
	}
}

func TestEncryptNonStringContent(t *testing.T) {
	input := `local x = 42; print(x)`
	output, err := Encrypt(input)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if !strings.Contains(output, "local x = 42") {
		t.Error("non-string Lua code should be preserved exactly")
	}
	if !strings.Contains(output, "print(x)") {
		t.Error("function calls should be preserved")
	}
}

func TestEncryptSingleQuoteString(t *testing.T) {
	input := `local s = 'hello'`
	output, err := Encrypt(input)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if strings.Contains(output, "'hello'") {
		t.Error("single-quoted string should be encoded")
	}
}

func TestEncryptRoundTrip(t *testing.T) {
	// Use proper Lua escape sequences (not raw control characters which are
	// illegal inside a Lua short string).
	input := `local s = "hello world\nwith\ttabs"`
	output, err := Encrypt(input)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	// The output should NOT contain the plain words verbatim (they are now
	// encoded as \ddd sequences).
	if strings.Contains(output, "hello world") {
		t.Error("original string content should not appear verbatim in encoded output")
	}
	// But it should contain a Lua string assignment.
	if !strings.Contains(output, "local s = ") {
		t.Error("structure should be preserved")
	}
	// The newline and tab bytes should appear as \ddd sequences.
	if !strings.Contains(output, `\010`) {
		t.Error("expected \\010 for newline byte in encoded output")
	}
	if !strings.Contains(output, `\009`) {
		t.Error("expected \\009 for tab byte in encoded output")
	}
}
