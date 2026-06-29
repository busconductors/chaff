package wrap

import (
	"fmt"
	"math/rand"
	"strings"
)

const z4Function = `local z4 = function(Z)
    local b = {}
    for s = 1, #Z / 2, 1 do
        b[#b + 1] = Z[#Z / 2 + Z[s]]
    end
    return table.concat(b)
end`

// Wrap wraps the input Lua payload in a 2-level nested IIFE with environment
// passing and the z4 string-reassembly function. The z4 function is injected
// here (Stage 5) so it sits OUTSIDE the CFG while-b-do loop.
//
// Pattern:
//   return (function(...)
//       local z4 = function(Z) ... end
//       return (function(22_params)
//           -- payload --
//           return (B(...))(s(d))
//       end)(env, unpack, newproxy, ...)
//   end)(...)
func Wrap(input string, seed int64) string {
	rng := rand.New(rand.NewSource(seed))

	// Generate 22 parameter names (1-2 chars each, A-Za-z only, no numbers).
	params := generateParamNames(22, 2, rng)

	// The 7 environment arguments passed to the inner IIFE.
	envArgs := []string{
		"getfenv and getfenv() or _ENV",
		"unpack or table.unpack",
		"newproxy",
		"setmetatable",
		"getmetatable",
		"select",
		"{...}",
	}

	// Decoy arithmetic: random-looking but deterministic expression.
	d1 := 500000 + rng.Intn(100000)
	d2 := 10000000 + rng.Intn(10000000)
	d3 := 100000 + rng.Intn(100000)

	var sb strings.Builder
	sb.WriteString("return (function(...)\n")
	sb.WriteString("    " + z4Function + "\n")
	sb.WriteString("    return (function(")
	sb.WriteString(strings.Join(params, ", "))
	sb.WriteString(")\n")
	sb.WriteString("        " + input + "\n")
	sb.WriteString(fmt.Sprintf("        return (B(%d + (%d - %d), {}))(s(d))\n", d1, d2, d3))
	sb.WriteString("    end)(")
	sb.WriteString(strings.Join(envArgs, ", "))
	sb.WriteString(")\n")
	sb.WriteString("end)(...)")

	return sb.String()
}

// generateParamNames produces N distinct parameter names of up to maxLen
// characters each, using A-Za-z only (no numbers).
func generateParamNames(n, maxLen int, rng *rand.Rand) []string {
	names := make([]string, n)
	used := make(map[string]bool)

	for i := 0; i < n; i++ {
		var name string
		for {
			l := 1 + rng.Intn(maxLen)
			b := make([]byte, l)
			for j := 0; j < l; j++ {
				if rng.Intn(2) == 0 {
					b[j] = byte('A' + rng.Intn(26))
				} else {
					b[j] = byte('a' + rng.Intn(26))
				}
			}
			name = string(b)
			if !used[name] {
				used[name] = true
				break
			}
		}
		names[i] = name
	}
	return names
}
