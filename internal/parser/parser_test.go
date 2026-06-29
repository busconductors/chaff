package parser

import (
	"strings"
	"testing"
)

func TestTokenizeShortStrings(t *testing.T) {
	input := `print("hello world")`
	tokens, err := Tokenize(input)
	if err != nil {
		t.Fatalf("Tokenize: %v", err)
	}
	found := false
	for _, tok := range tokens {
		if tok.Kind == TokString && tok.Value == "hello world" {
			found = true
		}
	}
	if !found {
		t.Error("expected token with value 'hello world'")
	}
}

func TestTokenizeEscapeSequences(t *testing.T) {
	input := `local s = "line1\nline2\ttab"`
	tokens, err := Tokenize(input)
	if err != nil {
		t.Fatalf("Tokenize: %v", err)
	}
	for _, tok := range tokens {
		if tok.Kind == TokString {
			if !strings.Contains(tok.Value, "\n") {
				t.Error("expected newline byte in decoded string")
			}
			if !strings.Contains(tok.Value, "\t") {
				t.Error("expected tab byte in decoded string")
			}
		}
	}
}

func TestTokenizeLongString(t *testing.T) {
	input := `local s = [[hello
world]]`
	tokens, err := Tokenize(input)
	if err != nil {
		t.Fatalf("Tokenize: %v", err)
	}
	for _, tok := range tokens {
		if tok.Kind == TokString {
			if !strings.Contains(tok.Value, "hello") {
				t.Error("expected 'hello' in long string content")
			}
			if !strings.Contains(tok.Value, "\n") {
				t.Error("expected newline in long string content")
			}
		}
	}
}

func TestTokenizeNestedLongString(t *testing.T) {
	input := `local s = [=[hello [world] more]=]`
	tokens, err := Tokenize(input)
	if err != nil {
		t.Fatalf("Tokenize: %v", err)
	}
	for _, tok := range tokens {
		if tok.Kind == TokString {
			if !strings.Contains(tok.Value, "[world]") {
				t.Errorf("expected '[world]' in nested long string, got: %q", tok.Value)
			}
		}
	}
}

func TestTokenizeKeywords(t *testing.T) {
	input := `local function foo() if true then return end end`
	tokens, err := Tokenize(input)
	if err != nil {
		t.Fatalf("Tokenize: %v", err)
	}
	kwCount := 0
	for _, tok := range tokens {
		if tok.Kind == TokKeyword {
			kwCount++
		}
	}
	if kwCount < 5 {
		t.Errorf("expected >= 5 keywords, got %d", kwCount)
	}
}

func TestTokenizeComments(t *testing.T) {
	input := `-- this is a comment
print("hello") --[[ block comment ]]`
	tokens, err := Tokenize(input)
	if err != nil {
		t.Fatalf("Tokenize: %v", err)
	}
	commentCount := 0
	for _, tok := range tokens {
		if tok.Kind == TokComment {
			commentCount++
		}
	}
	if commentCount != 2 {
		t.Errorf("expected 2 comments, got %d", commentCount)
	}
}

func TestTokenizeEmptyString(t *testing.T) {
	input := `local s = ""`
	tokens, err := Tokenize(input)
	if err != nil {
		t.Fatalf("Tokenize: %v", err)
	}
	for _, tok := range tokens {
		if tok.Kind == TokString && tok.Value != "" {
			t.Error("expected empty string content for \"\"")
		}
	}
}

func TestTokenizeConcatStrings(t *testing.T) {
	input := `local s = "hello" .. " world"`
	tokens, err := Tokenize(input)
	if err != nil {
		t.Fatalf("Tokenize: %v", err)
	}
	strCount := 0
	for _, tok := range tokens {
		if tok.Kind == TokString {
			strCount++
		}
	}
	if strCount != 2 {
		t.Errorf("expected 2 string tokens, got %d", strCount)
	}
}
