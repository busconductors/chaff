package main

import (
	"crypto/rand"
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/busconductors/chaff/internal/cfg"
	"github.com/busconductors/chaff/internal/deadcode"
	"github.com/busconductors/chaff/internal/encrypt"
	"github.com/busconductors/chaff/internal/shuffle"
	"github.com/busconductors/chaff/internal/wrap"
)

func main() {
	inputPath := flag.String("input", "", "Input Lua file (required)")
	outputPath := flag.String("output", "", "Output obfuscated Lua file (required)")
	seedFlag := flag.Int64("seed", 0, "Random seed for reproducibility (0 = random)")
	bloat := flag.Int("bloat", 3, "Dead code multiplier 1-5 (default: 3)")
	antiDebug := flag.Bool("anti-debug", false, "Inject debugger detection + timeout")
	stages := flag.String("stages", "1,2,3,4,5", "Comma-separated stages to run")
	verbose := flag.Bool("verbose", false, "Print stage-by-stage stats")
	flag.Parse()

	if *inputPath == "" || *outputPath == "" {
		fmt.Fprintln(os.Stderr, "Usage: chaff --input <file> --output <file> [flags]")
		flag.PrintDefaults()
		os.Exit(1)
	}

	// Read input.
	input, err := os.ReadFile(*inputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "chaff: read input: %v\n", err)
		os.Exit(1)
	}

	// Resolve seed.
	seed := *seedFlag
	if seed == 0 {
		var buf [8]byte
		if _, err := rand.Read(buf[:]); err == nil {
			seed = int64(binary.BigEndian.Uint64(buf[:])) & 0x7FFFFFFFFFFFFFFF
		} else {
			seed = 42
		}
	}

	// Parse which stages to run.
	stageSet := parseStages(*stages)

	// Pipeline.
	output := string(input)
	var stats []stageStat

	stagesToRun := []struct {
		name string
		num  int
		fn   func(string, int64) (string, error)
	}{
		{"encrypt", 1, func(s string, _ int64) (string, error) {
			return encrypt.Encrypt(s)
		}},
		{"shuffle", 2, func(s string, seed int64) (string, error) {
			return shuffle.Shuffle(s, seed)
		}},
		{"cfg", 3, func(s string, seed int64) (string, error) {
			return cfg.Flatten(s, seed)
		}},
		{"deadcode", 4, func(s string, seed int64) (string, error) {
			return deadcode.Inject(s, *bloat, seed), nil
		}},
		{"wrap", 5, func(s string, seed int64) (string, error) {
			return wrap.Wrap(s, seed), nil
		}},
	}

	for _, s := range stagesToRun {
		if !stageSet[s.num] {
			continue
		}
		inLen := len(output)
		result, err := s.fn(output, seed)
		if err != nil {
			fmt.Fprintf(os.Stderr, "chaff: Stage %d (%s): %v\n", s.num, s.name, err)
			os.Exit(1)
		}
		output = result
		outLen := len(output)
		stats = append(stats, stageStat{
			name:  s.name,
			num:   s.num,
			in:    inLen,
			out:   outLen,
			lines: strings.Count(output, "\n") + 1,
		})
	}

	// Anti-debug injection.
	if *antiDebug {
		output = injectAntiDebug(output)
	}

	// Write output.
	if err := os.WriteFile(*outputPath, []byte(output), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "chaff: write output: %v\n", err)
		os.Exit(1)
	}

	// Verbose stats.
	if *verbose {
		printStats(stats, *outputPath, len(output), strings.Count(output, "\n")+1)
	}
}

type stageStat struct {
	name  string
	num   int
	in    int
	out   int
	lines int
}

func printStats(stats []stageStat, path string, totalBytes, totalLines int) {
	for _, s := range stats {
		delta := s.out - s.in
		sign := "+"
		if delta < 0 {
			sign = ""
		}
		fmt.Printf("Stage %d (%s): %d → %d bytes (%s%d), %d lines\n",
			s.num, s.name, s.in, s.out, sign, delta, s.lines)
	}
	fmt.Printf("Output: %s (%d KB, %d lines)\n",
		path, totalBytes/1024, totalLines)
}

func parseStages(s string) map[int]bool {
	result := make(map[int]bool)
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if n, err := strconv.Atoi(part); err == nil && n >= 1 && n <= 5 {
			result[n] = true
		}
	}
	return result
}

func injectAntiDebug(input string) string {
	antiDebug := `
if debug and debug.gethook and debug.sethook then
    debug.sethook(function() error("debugger detected") end, "l")
end
local __chaff_start__ = os.clock()
`
	timeout := `
if os.clock() - __chaff_start__ > 60 then os.exit(1) end
`
	// Insert anti-debug at start, timeout before end.
	// Find insertion point: after the first IIFE opening.
	return antiDebug + input + timeout
}
