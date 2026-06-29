package deadcode

import (
	"fmt"
	"math/rand"
	"strings"
)

// Inject adds opaque predicates and fake local variable declarations to the
// input Lua source. The amount of dead code is controlled by the bloat level
// (1-5). seed ensures deterministic output.
func Inject(input string, bloat int, seed int64) string {
	if bloat < 1 {
		bloat = 1
	}
	if bloat > 5 {
		bloat = 5
	}

	rng := rand.New(rand.NewSource(seed))

	// Bloat lines per level: 1=50, 2=100, 3=250, 4=400, 5=1000
	lines := []int{0, 50, 100, 250, 400, 1000}[bloat]

	var sb strings.Builder
	sb.WriteString(input)
	sb.WriteString("\n")

	for i := 0; i < lines; i++ {
		sb.WriteString(generateDeadLine(rng))
		sb.WriteString("\n")
	}

	return sb.String()
}

func generateDeadLine(rng *rand.Rand) string {
	switch rng.Intn(6) {
	case 0:
		// Fake local declaration with arithmetic.
		name := randIdent(4, rng)
		a := rng.Intn(999999)
		b := rng.Intn(999999)
		c := rng.Int63n(99999999999999)
		op := []string{"+", "-", "*"}[rng.Intn(3)]
		return fmt.Sprintf("local %s = (%d %s %d) %s %d", name, a, op, b, op, c)
	case 1:
		// Multi-variable fake local.
		names := make([]string, rng.Intn(5)+2)
		for j := range names {
			names[j] = randIdent(4, rng)
		}
		return fmt.Sprintf("local %s", strings.Join(names, ", "))
	case 2:
		// Fake arithmetic chain.
		a := rng.Intn(999999)
		b := rng.Intn(999999)
		c := rng.Intn(999999)
		return fmt.Sprintf("local %s = %d + (-%d + %d)", randIdent(4, rng), a, b, c)
	case 3:
		// Opaque predicate: always-true condition.
		a := rng.Intn(500000)
		b := rng.Intn(500000)
		return fmt.Sprintf("if %d < (%d + %d) then local %s = %d end", a, a+b+1, rng.Intn(1000), randIdent(4, rng), rng.Intn(9999))
	case 4:
		// Fake assignment.
		return fmt.Sprintf("local %s = %d; %s = %s", randIdent(4, rng), rng.Intn(99999), randIdent(4, rng), randIdent(4, rng))
	default:
		// Fake table access.
		return fmt.Sprintf("local %s = Z[%d]", randIdent(4, rng), rng.Intn(100))
	}
}

const identChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

func randIdent(n int, rng *rand.Rand) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = identChars[rng.Intn(len(identChars))]
	}
	return string(b)
}
