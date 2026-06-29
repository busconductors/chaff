package encrypt

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/busconductors/chaff/internal/parser"
)

// Encrypt converts all string literals in Lua source to \ddd decimal-encoded
// byte sequences. Non-string syntax is preserved exactly.
// Returns an error if the input cannot be tokenized.
func Encrypt(input string) (string, error) {
	tokens, err := parser.Tokenize(input)
	if err != nil {
		return "", fmt.Errorf("tokenize: %w", err)
	}

	type replacement struct {
		start, end int    // byte offsets in input
		text       string // replacement text including quotes/brackets
	}

	var replacements []replacement
	for _, tok := range tokens {
		if tok.Kind != parser.TokString {
			continue
		}

		// Find end position of the raw string literal in source.
		end := findStringEnd(input, tok.Pos, tok.QuoteStyle)
		if end < 0 {
			continue
		}

		// Check if this string is already in \ddd form.
		if isAlreadyEncoded(input, tok.Pos, tok.QuoteStyle) {
			continue
		}

		// Encode the decoded byte content to \ddd sequences.
		encoded := encodeBytes([]byte(tok.Value))

		var replText string
		switch tok.QuoteStyle {
		case "\"", "'":
			replText = tok.QuoteStyle + encoded + tok.QuoteStyle
		default:
			// Long bracket: [[, [=[, [==[, etc.
			replText = tok.QuoteStyle + encoded + closingBracket(tok.QuoteStyle)
		}

		replacements = append(replacements, replacement{
			start: tok.Pos,
			end:   end,
			text:  replText,
		})
	}

	// Apply replacements in order (tokens are in source order).
	var result strings.Builder
	lastEnd := 0
	for _, r := range replacements {
		result.WriteString(input[lastEnd:r.start])
		result.WriteString(r.text)
		lastEnd = r.end
	}
	result.WriteString(input[lastEnd:])
	return result.String(), nil
}

// findStringEnd returns the byte offset just after the closing delimiter
// of a string literal starting at pos, or -1 if not found.
func findStringEnd(input string, pos int, quoteStyle string) int {
	switch quoteStyle {
	case "\"", "'":
		return findShortStringEnd(input, pos, quoteStyle[0])
	default:
		// Long bracket: [[, [=[, etc.
		return findLongStringEnd(input, pos, quoteStyle)
	}
}

// findShortStringEnd scans from the opening quote at pos and returns the
// byte offset just after the closing quote.
func findShortStringEnd(input string, pos int, quote byte) int {
	i := pos + 1 // skip opening quote
	for i < len(input) {
		b := input[i]
		if b == '\\' {
			i++ // skip backslash
			if i < len(input) {
				// If followed by digit(s), this is a \ddd decimal escape.
				// Skip up to 3 decimal digits.
				if input[i] >= '0' && input[i] <= '9' {
					for j := 0; j < 3 && i < len(input) && input[i] >= '0' && input[i] <= '9'; j++ {
						i++
					}
				} else {
					i++ // skip the single escaped character
				}
			}
			continue
		}
		if b == quote {
			i++ // include closing quote
			return i
		}
		// Advance past this character (handle multi-byte UTF-8).
		if b < 0x80 {
			i++
		} else {
			_, size := utf8.DecodeRuneInString(input[i:])
			if size == 0 {
				i++ // invalid UTF-8, advance one byte
			} else {
				i += size
			}
		}
	}
	return -1
}

// findLongStringEnd scans from the opening long bracket at pos and returns
// the byte offset just after the closing long bracket.
func findLongStringEnd(input string, pos int, openBracket string) int {
	closeBracket := closingBracket(openBracket)
	search := input[pos+len(openBracket):]
	idx := strings.Index(search, closeBracket)
	if idx < 0 {
		return -1
	}
	return pos + len(openBracket) + idx + len(closeBracket)
}

// closingBracket returns the closing long bracket for a given opener.
// e.g., "[[" -> "]]", "[=[" -> "]=]", "[==[" -> "]==]"
func closingBracket(opening string) string {
	eqCount := strings.Count(opening, "=")
	return "]" + strings.Repeat("=", eqCount) + "]"
}

// isAlreadyEncoded returns true if the string literal at pos appears to
// already consist of \ddd sequences (content starts with backslash).
func isAlreadyEncoded(input string, pos int, quoteStyle string) bool {
	switch quoteStyle {
	case "\"", "'":
		// After the opening quote, check if first char is backslash.
		return pos+1 < len(input) && input[pos+1] == '\\'
	default:
		// Long bracket: after the opening bracket, check first char.
		prefixLen := len(quoteStyle)
		return pos+prefixLen < len(input) && input[pos+prefixLen] == '\\'
	}
}

// encodeBytes converts each byte to a \ddd 3-digit zero-padded decimal escape.
func encodeBytes(b []byte) string {
	var buf strings.Builder
	for n := range b {
		fmt.Fprintf(&buf, "\\%03d", b[n])
	}
	return buf.String()
}
