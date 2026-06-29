# chaff

5-stage Lua obfuscator. VX Underground SmartLoader parity. Single binary, zero dependencies.

Takes plain Lua payloads and outputs heavily obfuscated Lua: decimal-encoded strings, z4 index-shuffled runtime reassembly, binary-tree control-flow flattening with per-block `do...end` scoping, opaque predicate dead code injection with configurable bloat, and nested IIFE wrapping with environment passing. Deterministic — same input + same seed = same output.

## Quick start

```bash
git clone https://github.com/busconductors/chaff.git && cd chaff
go build -o bin/chaff ./cmd/chaff/
./bin/chaff --input payloads/hello.lua --output /tmp/hello-obf.lua --seed 42 --verbose
luajit /tmp/hello-obf.lua
```

## Install

```bash
go install github.com/busconductors/chaff/cmd/chaff@latest
```

Pre-built binaries on [GitHub Releases](https://github.com/busconductors/chaff/releases).

## Usage

```
chaff --input <file> --output <file> [flags]

Flags:
  --input       string   Input Lua file (required)
  --output      string   Output obfuscated Lua file (required)
  --seed        int      Random seed for reproducibility (0 = random from crypto/rand)
  --bloat       int      Dead code multiplier 1-5 (default: 3)
  --anti-debug  bool     Inject debugger detection + timeout (default: false)
  --stages      string   Comma-separated stages to run (default: "1,2,3,4,5")
  --verbose     bool     Print stage-by-stage byte/line stats
```

### Example output

```
$ chaff --input payloads/hello.lua --output /tmp/obf.lua --seed 42 --verbose
Stage 1 (encrypt):   53 B → 98 B (+45), 4 lines
Stage 2 (shuffle):   98 B → 227 B (+129), 4 lines
Stage 3 (cfg):       227 B → 369 B (+142), 28 lines
Stage 4 (deadcode):  369 B → 9252 B (+8883), 279 lines
Stage 5 (wrap):      9252 B → 9714 B (+462), 291 lines
Output: /tmp/obf.lua (9 KB, 291 lines)
```

## Pipeline

```
Input ──→ [1:encrypt] ──→ [2:shuffle] ──→ [3:cfg] ──→ [4:deadcode] ──→ [5:wrap] ──→ Output
          \ddd bytes     z4() calls     while-b-do     opaque preds     IIFE nest
```

### Stage 1 — String Encryption
Every Lua string literal → `\ddd` decimal-encoded byte sequences. Uses a char-by-char state-machine parser that correctly handles short strings, long brackets with `=` nesting, escape sequences, and comments.

### Stage 2 — Index Shuffle
Each encoded string split into 2-5 chunks, indices permuted, replaced with `z4({indices; chunk1, chunk2, ...})` runtime-reassembly calls.

### Stage 3 — Control-Flow Flattening
Recursive-descent Lua parser decomposes code into basic blocks. All `local` declarations hoisted before `while b do`. Blocks wrapped in `do...end`. Binary decision tree dispatch. Terminal block uses `return` only. Handles `if/elseif/else`, `while`, `for`, `repeat...until`, `return`, `break`.

### Stage 4 — Dead Code Injection
Opaque predicates + fake local variable declarations interleaved between real blocks. 6 template types. Controlled by `--bloat 1-5` (50 → 1000 lines).

### Stage 5 — IIFE Wrap + z4
2-level nested IIFE with 22 environment params. z4 function injected here (outside the CFG loop) using `table.concat()` for O(n) string assembly. Decoy return line. A-Za-z parameter names.

## Design

- **Deterministic**: same input + same seed = same output at every stage
- **Functional equivalence**: obfuscated payload produces identical stdout
- **LuaJIT 2.1+ compatible** (PUC Lua 5.1+ where possible)
- **Pure Go, zero dependencies** — stdlib only, `go build` is all you need
- **Single file output** — everything inline, no `require` calls
- **Fail-fast errors** — `Stage N (name): reason`, exit code 1

## Architecture

```
chaff/
├── cmd/chaff/main.go               CLI entrypoint, pipeline orchestration
├── internal/
│   ├── parser/                      Lua tokenizer (state-machine)
│   ├── encrypt/                     Stage 1 — string → \ddd bytes
│   ├── shuffle/                     Stage 2 — z4 chunk permutation
│   ├── cfg/                         Stage 3 — while-b-do CFG flattening
│   ├── deadcode/                    Stage 4 — opaque predicates + bloat
│   └── wrap/                        Stage 5 — IIFE nesting + z4 injection
├── payloads/                        Test fixtures (hello, download-cradle, persistence)
├── .github/workflows/release.yml    CI/CD — cross-compile, test, GitHub Releases
└── docs/superpowers/                Design spec + implementation plan
```

## Tests

```
6 packages, 52 tests, 3477 LOC
go test ./... -v -count=1   # all PASS
```

| Package | Tests | Stage |
|---------|-------|-------|
| `internal/parser/` | 8 | Shared tokenizer |
| `internal/encrypt/` | 9 | Stage 1 |
| `internal/shuffle/` | 6 | Stage 2 |
| `internal/cfg/` | 16 | Stage 3 |
| `internal/deadcode/` | 6 | Stage 4 |
| `internal/wrap/` | 7 | Stage 5 |

## Reference

VX Underground SmartLoader pattern: `vxunderground/91da9c50e400a6742bbacd1548a255d8`
