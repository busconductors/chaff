package parser

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Token represents a single Lua token with position info.
type Token struct {
	Kind       TokenKind
	Value      string
	Line       int    // 1-based line number
	Pos        int    // byte offset in source
	QuoteStyle string // set for TokString: "'", "\"", or "[[", "[=[", etc.
}

// TokenKind classifies the type of Lua token.
type TokenKind int

const (
	TokString TokenKind = iota
	TokComment
	TokKeyword
	TokIdent
	TokNumber
	TokOperator
	TokWhitespace
	TokNewline
	TokOther
)

// StringLiteral is an extracted Lua string literal with position info.
type StringLiteral struct {
	StartPos   int    // byte offset in source
	EndPos     int    // byte offset in source (exclusive)
	Content    string // decoded string content (without quotes)
	QuoteStyle string // "'", "\"", or "[[", "[=[" etc.
}

// Tokenize converts Lua source into a token stream.
// Uses a char-by-char state machine to handle strings, comments, and operators.
func Tokenize(input string) ([]Token, error) {
	var tokens []Token
	line := 1
	pos := 0
	runes := []rune(input)

	// build a rune→byte-offset map for position tracking
	runeToByte := make([]int, len(runes)+1)
	runeIdx := 0
	for byteIdx := range input {
		runeToByte[runeIdx] = byteIdx
		runeIdx++
	}
	runeToByte[len(runes)] = len(input)

	runeToBytePos := func(runePos int) int {
		if runePos < 0 {
			return 0
		}
		if runePos > len(runes) {
			return len(input)
		}
		return runeToByte[runePos]
	}

	for pos < len(runes) {
		r := runes[pos]
		bytePos := runeToBytePos(pos)

		switch {
		case r == '\n':
			tokens = append(tokens, Token{Kind: TokNewline, Value: "\n", Line: line, Pos: bytePos})
			line++
			pos++
			continue

		case r == ' ' || r == '\t' || r == '\r':
			pos++
			continue

		case r == '-' && pos+1 < len(runes) && runes[pos+1] == '-':
			tok, newPos, err := lexComment(input, runes, pos, line, bytePos, runeToBytePos)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, tok)
			pos = newPos
			continue

		case r == '"' || r == '\'':
			tok, newPos, err := lexShortString(input, runes, pos, line, bytePos, runeToBytePos)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", line, err)
			}
			tokens = append(tokens, tok)
			pos = newPos
			continue

		case r == '[' && pos+1 < len(runes) && (runes[pos+1] == '[' || runes[pos+1] == '='):
			// Long bracket: [[, [=[, [==[, etc.
			tok, newPos, isLong, err := lexLongBracket(runes, pos, line, bytePos)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", line, err)
			}
			if isLong {
				tokens = append(tokens, tok)
				pos = newPos
				continue
			}
			// Not a long bracket, treat [ as operator
			tokens = append(tokens, Token{Kind: TokOperator, Value: "[", Line: line, Pos: bytePos})
			pos++
			continue

		case isIdentStart(r):
			tok, newPos := lexIdent(input, runes, pos, line, bytePos, runeToBytePos)
			tokens = append(tokens, tok)
			pos = newPos
			continue

		case isDigit(r) || (r == '.' && pos+1 < len(runes) && isDigit(runes[pos+1])):
			tok, newPos := lexNumber(runes, pos, line, bytePos, runeToBytePos)
			tokens = append(tokens, tok)
			pos = newPos
			continue

		case isOperatorChar(r):
			tok, newPos := lexOperator(runes, pos, line, bytePos, runeToBytePos)
			tokens = append(tokens, tok)
			pos = newPos
			continue

		default:
			tokens = append(tokens, Token{Kind: TokOther, Value: string(r), Line: line, Pos: bytePos})
			pos++
		}
	}

	return tokens, nil
}

// ExtractStrings returns all string literals from a token stream.
func ExtractStrings(tokens []Token) []StringLiteral {
	var result []StringLiteral
	for _, tok := range tokens {
		if tok.Kind == TokString {
			sl := StringLiteral{
				Content:    tok.Value,
				QuoteStyle: tok.QuoteStyle,
				StartPos:   tok.Pos,
				// EndPos is the byte position just after the string literal.
				// Compute it from StartPos + raw length in source.
				// The raw length includes quotes and = padding.
				// For short strings: Content + 2 (quotes) + escape overhead
				// For long strings: Content + opener + closer length
				// We store a conservative EndPos = StartPos + 2 + len(Content)
				// which works for short strings. Callers needing precision for
				// long strings should derive it from Token metadata.
			}
			// Estimate EndPos from content length + quote overhead.
			// This is an approximation; exact end position requires raw source access.
			switch tok.QuoteStyle {
			case "'", "\"":
				// Short string: 2 quote chars + escape expansions.
				// We stored Pos at the opening quote; for simple strings
				// the end is approximately Pos + rawLen where rawLen varies.
				// Provide a best-effort estimate.
				sl.EndPos = tok.Pos + len(tok.QuoteStyle)*2 + len(tok.Value)
			default:
				// Long string: [[...]] or [=[...]=]
				sl.EndPos = tok.Pos + len(tok.QuoteStyle) + len(tok.Value)
			}
			result = append(result, sl)
		}
	}
	return result
}

// --- lexer helpers ---

func lexShortString(
	src string,
	runes []rune,
	pos, line, bytePos int,
	runeToBytePos func(int) int,
) (Token, int, error) {
	quote := runes[pos]
	quoteStr := string(quote)
	end := pos + 1
	var buf strings.Builder

	for end < len(runes) {
		r := runes[end]
		if r == '\n' {
			return Token{}, pos, fmt.Errorf("unterminated string")
		}
		if r == '\\' && end+1 < len(runes) {
			next := runes[end+1]
			switch next {
			case 'n':
				buf.WriteByte('\n')
			case 't':
				buf.WriteByte('\t')
			case 'r':
				buf.WriteByte('\r')
			case '\\':
				buf.WriteByte('\\')
			case '"':
				buf.WriteByte('"')
			case '\'':
				buf.WriteByte('\'')
			case '0':
				buf.WriteByte(0)
			default:
				if isDigit(next) {
					// \ddd decimal escape sequence
					decoded, n := lexDecimalEscape(runes, end+1)
					buf.WriteByte(decoded)
					end += n
					continue
				}
				// unrecognized escape, keep literal
				buf.WriteByte(byte(next))
			}
			end += 2
			continue
		}
		if r == quote {
			end++
			break
		}
		_, size := utf8.DecodeRuneInString(string(runes[end:]))
		buf.WriteString(string(runes[end : end+size]))
		end += size
	}
	return Token{
		Kind:       TokString,
		Value:      buf.String(),
		Line:       line,
		Pos:        bytePos,
		QuoteStyle: quoteStr,
	}, end, nil
}

// lexLongBracket attempts to parse a long bracket (string or comment).
// Returns isLong=false if the character sequence is not a valid long bracket opener.
func lexLongBracket(
	runes []rune,
	pos, line, bytePos int,
) (Token, int, bool, error) {
	// Long bracket syntax: [ + =*N + [
	// Level 0: [[  Level 1: [=[  Level 2: [==[  etc.
	// pos points to the first '['
	eqCount := 0
	i := pos + 1 // skip first '['
	for i < len(runes) && runes[i] == '=' {
		eqCount++
		i++
	}
	if i >= len(runes) || runes[i] != '[' {
		// Not a valid long bracket opener
		return Token{}, pos, false, nil
	}
	i++ // skip second '['

	quoteStyle := "[" + strings.Repeat("=", eqCount) + "["

	// Build closing pattern: ] + eqCount*'=' + ]
	closing := "]" + strings.Repeat("=", eqCount) + "]"

	var buf strings.Builder
	startLine := line
	for i < len(runes) {
		rest := string(runes[i:])
		if strings.HasPrefix(rest, closing) {
			i += len([]rune(closing))
			break
		}
		if runes[i] == '\n' {
			line++
		}
		buf.WriteRune(runes[i])
		i++
	}
	return Token{
		Kind:       TokString,
		Value:      buf.String(),
		Line:       startLine,
		Pos:        bytePos,
		QuoteStyle: quoteStyle,
	}, i, true, nil
}

func lexComment(
	src string,
	runes []rune,
	pos, line, bytePos int,
	runeToBytePos func(int) int,
) (Token, int, error) {
	i := pos + 2 // skip --
	if i < len(runes) && runes[i] == '[' {
		// Long comment [[...]], handle = nesting
		eqCount := 0
		i++
		for i < len(runes) && runes[i] == '=' {
			eqCount++
			i++
		}
		if i < len(runes) && runes[i] == '[' {
			i++
			closing := "]" + strings.Repeat("=", eqCount) + "]"
			var buf strings.Builder
			for i < len(runes) {
				rest := string(runes[i:])
				if strings.HasPrefix(rest, closing) {
					i += len([]rune(closing))
					break
				}
				if runes[i] == '\n' {
					line++
				}
				buf.WriteRune(runes[i])
				i++
			}
			return Token{
				Kind:  TokComment,
				Value: buf.String(),
				Line:  line,
				Pos:   bytePos,
			}, i, nil
		}
		// Not a long comment (just --[), fall through to short comment
		// Reset i to after -- to process normally
		i = pos + 2
	}
	// Short comment: consume to end of line
	var buf strings.Builder
	for i < len(runes) && runes[i] != '\n' {
		buf.WriteRune(runes[i])
		i++
	}
	return Token{
		Kind:  TokComment,
		Value: buf.String(),
		Line:  line,
		Pos:   bytePos,
	}, i, nil
}

func lexIdent(
	src string,
	runes []rune,
	pos, line, bytePos int,
	runeToBytePos func(int) int,
) (Token, int) {
	i := pos
	var buf strings.Builder
	for i < len(runes) && isIdentChar(runes[i]) {
		buf.WriteRune(runes[i])
		i++
	}
	val := buf.String()
	kind := TokIdent
	if isKeyword(val) {
		kind = TokKeyword
	}
	return Token{Kind: kind, Value: val, Line: line, Pos: bytePos}, i
}

func lexNumber(
	runes []rune,
	pos, line, bytePos int,
	runeToBytePos func(int) int,
) (Token, int) {
	i := pos
	var buf strings.Builder
	// Handle hex prefix
	if runes[i] == '0' && i+1 < len(runes) && (runes[i+1] == 'x' || runes[i+1] == 'X') {
		buf.WriteRune(runes[i])
		buf.WriteRune(runes[i+1])
		i += 2
		for i < len(runes) && isHexDigit(runes[i]) {
			buf.WriteRune(runes[i])
			i++
		}
		return Token{Kind: TokNumber, Value: buf.String(), Line: line, Pos: bytePos}, i
	}
	// Decimal number: integer part, optional fraction, optional exponent
	for i < len(runes) && isDigit(runes[i]) {
		buf.WriteRune(runes[i])
		i++
	}
	if i < len(runes) && runes[i] == '.' {
		buf.WriteRune(runes[i])
		i++
		for i < len(runes) && isDigit(runes[i]) {
			buf.WriteRune(runes[i])
			i++
		}
	}
	// exponent
	if i < len(runes) && (runes[i] == 'e' || runes[i] == 'E') {
		buf.WriteRune(runes[i])
		i++
		if i < len(runes) && (runes[i] == '+' || runes[i] == '-') {
			buf.WriteRune(runes[i])
			i++
		}
		for i < len(runes) && isDigit(runes[i]) {
			buf.WriteRune(runes[i])
			i++
		}
	}
	return Token{Kind: TokNumber, Value: buf.String(), Line: line, Pos: bytePos}, i
}

func lexOperator(
	runes []rune,
	pos, line, bytePos int,
	runeToBytePos func(int) int,
) (Token, int) {
	r := runes[pos]
	// Multi-char operators
	if pos+1 < len(runes) {
		two := string([]rune{r, runes[pos+1]})
		switch two {
		case "..", "==", "~=", "<=", ">=", "//":
			return Token{Kind: TokOperator, Value: two, Line: line, Pos: bytePos}, pos + 2
		}
	}
	return Token{Kind: TokOperator, Value: string(r), Line: line, Pos: bytePos}, pos + 1
}

func lexDecimalEscape(runes []rune, pos int) (byte, int) {
	// parse \ddd where ddd is 1-3 decimal digits
	val := 0
	count := 0
	for i := pos; i < len(runes) && i < pos+3 && isDigit(runes[i]); i++ {
		val = val*10 + int(runes[i]-'0')
		count++
	}
	return byte(val % 256), count
}

// --- character classification ---

func isIdentStart(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_'
}

func isIdentChar(r rune) bool {
	return isIdentStart(r) || isDigit(r)
}

func isDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

func isHexDigit(r rune) bool {
	return isDigit(r) || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

func isOperatorChar(r rune) bool {
	return strings.ContainsRune("+-*/%^#=<>(){}[];:,.#", r)
}

var keywords = map[string]bool{
	"and": true, "break": true, "do": true, "else": true, "elseif": true,
	"end": true, "false": true, "for": true, "function": true, "goto": true,
	"if": true, "in": true, "local": true, "nil": true, "not": true,
	"or": true, "repeat": true, "return": true, "then": true, "true": true,
	"until": true, "while": true,
}

func isKeyword(s string) bool { return keywords[s] }
