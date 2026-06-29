package shuffle

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"

	"github.com/busconductors/chaff/internal/parser"
)

// Shuffle replaces \ddd-encoded string literals with z4() calls that
// reassemble the byte chunks at runtime. z4 is defined by Stage 5 (wrap),
// Stage 2 only generates the z4() CALLS.
//
// Each encoded string is split into 2-5 chunks, the chunk indices are
// permuted, and the string literal is replaced with:
//
//	z4({indices; chunk1, chunk2, ...})
func Shuffle(input string, seed int64) (string, error) {
	tokens, err := parser.Tokenize(input)
	if err != nil {
		return "", fmt.Errorf("tokenize: %w", err)
	}

	rng := rand.New(rand.NewSource(seed))
	runes := []rune(input)
	lastEnd := 0

	type replacement struct {
		start, end int
		z4call     string
	}
	var repls []replacement

	scanPos := 0
	for _, tok := range tokens {
		if tok.Kind != parser.TokString {
			continue
		}

		// Find the string literal in source.
		start, end := findString(runes, scanPos)
		if start < 0 {
			continue
		}

		// Only process \ddd-encoded strings (not empty strings).
		if tok.Value == "" {
			scanPos = end
			continue
		}

		// Verify this is a \ddd-encoded string.
		if !isDecimalEncoded(runes, start) {
			scanPos = end
			continue
		}

		// Decode the \ddd bytes back to raw bytes.
		rawBytes := decodeDecimalString(tok.Value)
		if len(rawBytes) == 0 {
			scanPos = end
			continue
		}

		// Split into 2-5 chunks and permute.
		nChunks := 2 + rng.Intn(4) // 2-5
		chunks := splitChunks(rawBytes, nChunks, rng)
		indices := permuteIndices(nChunks, rng)

		// Build z4({indices; chunk1, chunk2, ..., chunkN}) call.
		z4call := buildZ4Call(indices, chunks)
		repls = append(repls, replacement{start, end, z4call})
		scanPos = end
	}

	// Apply replacements.
	var sb strings.Builder
	for _, r := range repls {
		sb.WriteString(string(runes[lastEnd:r.start]))
		sb.WriteString(r.z4call)
		lastEnd = r.end
	}
	sb.WriteString(string(runes[lastEnd:]))
	return sb.String(), nil
}

func findString(runes []rune, startFrom int) (int, int) {
	i := startFrom
	for i < len(runes) {
		r := runes[i]
		if r == '"' || r == '\'' {
			end := scanShortString(runes, i)
			if end > i {
				return i, end
			}
		}
		if r == '[' && i+1 < len(runes) && runes[i+1] == '[' {
			end := scanLongString(runes, i)
			if end > i {
				return i, end
			}
		}
		i++
	}
	return -1, -1
}

func scanShortString(runes []rune, start int) int {
	quote := runes[start]
	i := start + 1
	for i < len(runes) {
		if runes[i] == '\\' {
			i++ // skip escaped
		} else if runes[i] == quote {
			return i + 1
		}
		i++
	}
	return -1
}

func scanLongString(runes []rune, start int) int {
	eqCount := 0
	i := start + 2
	for i < len(runes) && runes[i] == '=' {
		eqCount++
		i++
	}
	if i >= len(runes) || runes[i] != '[' {
		return -1
	}
	i++
	closing := "]" + strings.Repeat("=", eqCount) + "]"
	for i < len(runes) {
		if strings.HasPrefix(string(runes[i:]), closing) {
			return i + len([]rune(closing))
		}
		i++
	}
	return -1
}

func isDecimalEncoded(runes []rune, strStart int) bool {
	// After the opening quote, check for \ddd pattern.
	if runes[strStart] == '"' || runes[strStart] == '\'' {
		if strStart+1 < len(runes) && runes[strStart+1] == '\\' {
			return true
		}
	}
	return false
}

func decodeDecimalString(encoded string) []byte {
	// Parse \ddd sequences back to raw bytes.
	var result []byte
	runes := []rune(encoded)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '\\' && i+3 < len(runes) {
			// Try to parse \ddd.
			d1 := int(runes[i+1] - '0')
			d2 := int(runes[i+2] - '0')
			d3 := int(runes[i+3] - '0')
			if d1 >= 0 && d1 <= 9 && d2 >= 0 && d2 <= 9 && d3 >= 0 && d3 <= 9 {
				val := d1*100 + d2*10 + d3
				result = append(result, byte(val%256))
				i += 3
				continue
			}
		}
		result = append(result, byte(runes[i]))
	}
	return result
}

func splitChunks(data []byte, n int, rng *rand.Rand) [][]byte {
	if n <= 1 || len(data) == 0 {
		return [][]byte{data}
	}
	chunks := make([][]byte, n)
	base := len(data) / n
	rem := len(data) % n
	pos := 0
	for i := 0; i < n; i++ {
		size := base
		if i < rem {
			size++
		}
		if pos+size > len(data) {
			size = len(data) - pos
		}
		chunks[i] = data[pos : pos+size]
		pos += size
	}
	return chunks
}

func permuteIndices(n int, rng *rand.Rand) []int {
	indices := make([]int, n)
	for i := range indices {
		indices[i] = i + 1 // 1-based
	}
	rng.Shuffle(n, func(i, j int) {
		indices[i], indices[j] = indices[j], indices[i]
	})
	return indices
}

func buildZ4Call(indices []int, chunks [][]byte) string {
	// z4({i1, i2, i3; "\ddd...", "\ddd...", "\ddd..."})
	var sb strings.Builder
	sb.WriteString("z4({")
	for i, idx := range indices {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(strconv.Itoa(idx))
	}
	sb.WriteString("; ")
	for i, chunk := range chunks {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(`"`)
		for _, b := range chunk {
			fmt.Fprintf(&sb, "\\%03d", b)
		}
		sb.WriteString(`"`)
	}
	sb.WriteString("})")
	return sb.String()
}
