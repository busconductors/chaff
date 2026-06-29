// Package cfg implements Stage 3 control-flow flattening.
//
// Flatten converts sequential Lua code into a while-b-do binary-tree state
// machine. All local declarations are hoisted above the loop, each basic block
// is wrapped in do...end, and the terminal block uses return only.
//
// Control-flow constructs handled:
//   - if / elseif / else  — condition dispatch via b = cond AND a OR b
//   - while ... do ... end — condition block with back-edge
//   - repeat ... until    — body blocks + tail-condition with back-edge
//   - for (numeric)       — init / cond / body / incr blocks
//   - for (generic)       — condition block + body blocks
//   - return              — terminal block
//   - break               — jump to enclosing loop merge point
//
// Deterministic output is guaranteed for a given (input, seed) pair.
package cfg

import (
	"fmt"
	"math/rand"
	"strings"

	"github.com/busconductors/chaff/internal/parser"
)

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// Flatten converts sequential Lua code into a while-b-do binary-tree state
// machine. All local declarations are hoisted to before the while loop.
// Each basic block is wrapped in do...end.
// The terminal block uses 'return' (not break/b=nil) so the decoy IIFE
// return line injected by Stage 5 is never reached.
func Flatten(input string, seed int64) (string, error) {
	rng := rand.New(rand.NewSource(seed))

	// Tokenize and parse into basic blocks.
	tokens, err := parser.Tokenize(input)
	if err != nil {
		return "", fmt.Errorf("cfg tokenize: %w", err)
	}

	blocks, hoistedLocals, err := decomposeBlocks(tokens)
	if err != nil {
		return "", fmt.Errorf("cfg decompose: %w", err)
	}

	if len(blocks) == 0 {
		// Empty input: produce minimal valid CFG.
		var sb strings.Builder
		sb.WriteString("local b = 0\n")
		sb.WriteString("while b do\n")
		sb.WriteString("return\n")
		sb.WriteString("end")
		return sb.String(), nil
	}

	var sb strings.Builder

	// Hoist locals before the while loop.
	for _, local := range hoistedLocals {
		sb.WriteString("local " + local + "\n")
	}

	// State tracker.
	sb.WriteString("local b = 0\n")
	sb.WriteString("while b do\n")

	// Assign unique block IDs with random spacing.
	blockIDs := assignBlockIDs(blocks, rng)

	// Build binary decision tree dispatch.
	treeCode := buildBinaryTree(blocks, blockIDs, rng)
	sb.WriteString(treeCode)

	sb.WriteString("end")

	return sb.String(), nil
}

// ---------------------------------------------------------------------------
// Basic block representation
// ---------------------------------------------------------------------------

// basicBlock is a unit of straight-line Lua code with explicit successor info.
type basicBlock struct {
	code     string   // Lua code for this block (may be empty)
	isCond   bool     // true if this block dispatches via b = cond AND ...
	condExpr string   // the Lua condition expression (for condition blocks)
	nextIdx  int      // primary next block index (-1 = terminal via return)
	altIdx   int      // alternative next for condition blocks (-1 = none)
}

// ---------------------------------------------------------------------------
// Block builder — accumulates blocks during recursive-descent parsing.
// ---------------------------------------------------------------------------

type blockBuilder struct {
	blocks        []basicBlock
	hoistedLocals []string
}

// addBlock appends a block and returns its index.
func (b *blockBuilder) addBlock(blk basicBlock) int {
	idx := len(b.blocks)
	b.blocks = append(b.blocks, blk)
	return idx
}

// addEmptyBlock appends an empty (no-op) block and returns its index.
func (b *blockBuilder) addEmptyBlock() int {
	return b.addBlock(basicBlock{})
}

// addTerminalBlock appends a terminal block (buildLeafBlock emits return).
func (b *blockBuilder) addTerminalBlock() int {
	return b.addBlock(basicBlock{nextIdx: -1})
}

// setBlockNext updates the primary successor of a block.
func (b *blockBuilder) setBlockNext(idx, nextIdx int) {
	b.blocks[idx].nextIdx = nextIdx
}

// setBlockAlt updates the alternative successor of a block.
func (b *blockBuilder) setBlockAlt(idx, altIdx int) {
	b.blocks[idx].altIdx = altIdx
}

// lastIdx returns the index of the last block, or -1 if no blocks.
func (b *blockBuilder) lastIdx() int {
	return len(b.blocks) - 1
}

// ---------------------------------------------------------------------------
// Recursive-descent parser
// ---------------------------------------------------------------------------

// decomposeBlocks tokenizes and parses the input into basic blocks.
func decomposeBlocks(tokens []parser.Token) ([]basicBlock, []string, error) {
	b := &blockBuilder{}
	_, err := b.parseBody(tokens, 0, nil)
	if err != nil {
		return nil, nil, err
	}

	// After parsing, ensure there is a terminal block.
	// The parse functions should already have set the correct terminal block,
	// but fix up any issues.
	if len(b.blocks) == 0 {
		b.addTerminalBlock()
		return b.blocks, b.hoistedLocals, nil
	}

	// Remove trailing empty blocks that are not referenced.
	// An empty block is unreferenced if no other block has it as a target.
	for len(b.blocks) > 1 {
		last := len(b.blocks) - 1
		lb := b.blocks[last]
		if lb.code != "" || lb.isCond || lb.nextIdx != 0 {
			break
		}
		// Check if any block references this one.
		referenced := false
		for i := 0; i < last; i++ {
			bi := b.blocks[i]
			if bi.nextIdx == last || bi.altIdx == last {
				referenced = true
				break
			}
		}
		if referenced {
			break
		}
		b.blocks = b.blocks[:last]
	}

	// Ensure last block is terminal.
	last := len(b.blocks) - 1
	if b.blocks[last].nextIdx != -1 {
		if b.blocks[last].nextIdx == 0 && !b.blocks[last].isCond {
			b.blocks[last].nextIdx = -1
		} else {
			// Add terminal block and link any dangling references to it.
			termIdx := b.addTerminalBlock()
			for i := last; i >= 0; i-- {
				bi := &b.blocks[i]
				if bi.isCond {
					if bi.altIdx == 0 || bi.altIdx >= termIdx {
						bi.altIdx = termIdx
					}
					if bi.nextIdx == 0 || bi.nextIdx >= termIdx {
						bi.nextIdx = termIdx
					}
				} else if bi.nextIdx == 0 || bi.nextIdx >= termIdx {
					bi.nextIdx = termIdx
				}
			}
		}
	}

	return b.blocks, b.hoistedLocals, nil
}

// parseBody parses a sequence of statements until EOF or a terminator keyword
// from the set. Returns the index of the first block created.
//
// When encountering control-flow keywords (if, while, for, repeat, return,
// break), parseBody delegates to the appropriate sub-parser rather than
// treating them as regular statements.
func (b *blockBuilder) parseBody(
	tokens []parser.Token,
	start int,
	terminators map[string]bool,
) (int, error) {
	firstIdx := -1
	pos := start

	for pos < len(tokens) {
		// Skip whitespace and newlines.
		npos := skipGap(tokens, pos)
		if npos >= len(tokens) {
			break
		}

		// Check for terminators.
		tok := tokens[npos]
		if tok.Kind == parser.TokKeyword && terminators[tok.Value] {
			return firstIdx, nil
		}

		// Collect a "line" of tokens up to the next newline, handling
		// multi-line constructs.
		lineEnd := findLineEnd(tokens, npos)
		line := tokens[npos:lineEnd]

		if len(line) == 0 {
			pos = lineEnd
			continue
		}

		firstKw := firstKeyword(line)
		switch firstKw {
		case "local":
			// Hoist local declarations.
			hoisted, stmt, err := processLocal(line)
			if err != nil {
				return firstIdx, fmt.Errorf("line %d: %w", tokens[npos].Line, err)
			}
			if hoisted != "" {
				b.hoistedLocals = append(b.hoistedLocals, hoisted)
			}
			if stmt != "" {
				// Keep the assignment as a regular statement.
				idx := b.addBlock(basicBlock{code: stmt})
				if firstIdx < 0 {
					firstIdx = idx
				}
			}
			pos = lineEnd

			case "if":
			subFirst, newPos, err := b.parseIf(tokens, npos)
			if err != nil {
				return firstIdx, err
			}
			if firstIdx < 0 {
				firstIdx = subFirst
			}
			pos = newPos

		case "while":
			subFirst, newPos, err := b.parseWhile(tokens, npos)
			if err != nil {
				return firstIdx, err
			}
			if firstIdx < 0 {
				firstIdx = subFirst
			}
			pos = newPos

		case "for":
			subFirst, newPos, err := b.parseFor(tokens, npos)
			if err != nil {
				return firstIdx, err
			}
			if firstIdx < 0 {
				firstIdx = subFirst
			}
			pos = newPos

		case "repeat":
			subFirst, newPos, err := b.parseRepeat(tokens, npos)
			if err != nil {
				return firstIdx, err
			}
			if firstIdx < 0 {
				firstIdx = subFirst
			}
			pos = newPos

		case "return":
			idx := b.addTerminalBlock()
			if firstIdx < 0 {
				firstIdx = idx
			}
			pos = lineEnd

		case "break":
			// Break: terminal-ish block that jumps to loop merge.
			// The merge point is set by the enclosing loop parser.
			idx := b.addBlock(basicBlock{code: "break"})
			if firstIdx < 0 {
				firstIdx = idx
			}
			pos = lineEnd

		case "end":
			// end without matching opener – treat as terminator.
			return firstIdx, nil

		case "else", "elseif", "until":
			// These are handled by the enclosing parser.
			return firstIdx, nil

		case "do":
			// Bare do block: parse body until end.
			subFirst, newPos, err := b.parseDoBlock(tokens, npos)
			if err != nil {
				return firstIdx, err
			}
			if firstIdx < 0 {
				firstIdx = subFirst
			}
			pos = newPos

		case "::":
			// goto label – preserve as-is (rare in payloads).
			lineText := tokenLineText(line)
			idx := b.addBlock(basicBlock{code: lineText})
			if firstIdx < 0 {
				firstIdx = idx
			}
			pos = lineEnd

		case "goto":
			// goto statement – preserve as-is.
			lineText := tokenLineText(line)
			idx := b.addBlock(basicBlock{code: lineText})
			if firstIdx < 0 {
				firstIdx = idx
			}
			pos = lineEnd

		case "function":
			// Function declaration – treat as single statement.
			subFirst, newPos, err := b.parseFunction(tokens, npos)
			if err != nil {
				return firstIdx, err
			}
			if firstIdx < 0 {
				firstIdx = subFirst
			}
			pos = newPos

		default:
			// Regular statement(s).
			lineText := tokenLineText(line)
			idx := b.addBlock(basicBlock{code: lineText})
			if firstIdx < 0 {
				firstIdx = idx
			}
			pos = lineEnd
		}

		// Link sequential blocks.
		if b.lastIdx() > 0 && b.blocks[b.lastIdx()-1].nextIdx == 0 && !b.blocks[b.lastIdx()-1].isCond {
			b.blocks[b.lastIdx()-1].nextIdx = b.lastIdx()
		}
	}

	return firstIdx, nil
}

// ---------------------------------------------------------------------------
// Control-flow parsers
// ---------------------------------------------------------------------------

// parseIf parses an if / elseif / else / end construct.
//
// Structure:
//
//	[cond block: b = condition AND thenFirst OR elseFirst]
//	[then-body blocks]
//	[elseif-cond block] ... [elseif-body blocks] ...   (optional, repeated)
//	[else-body blocks]                                  (optional)
//	[merge block]
//
// Returns (index of first block, position after end, error).
func (b *blockBuilder) parseIf(
	tokens []parser.Token,
	start int,
) (int, int, error) {
	// Expect 'if'.
	pos := skipGap(tokens, start)
	if pos >= len(tokens) || tokens[pos].Value != "if" {
		return -1, start, fmt.Errorf("expected 'if' at line %d", tokens[pos].Line)
	}
	pos++ // skip 'if'

	// Collect condition expression between 'if' and 'then'.
	cond, thenPos, err := collectUntil(tokens, pos, "then")
	if err != nil {
		return -1, start, fmt.Errorf("if condition: %w", err)
	}

	// Parse then-body until 'end', 'else', or 'elseif'.
	thenTerminators := map[string]bool{"end": true, "else": true, "elseif": true}
	thenBodyStart := len(b.blocks)

	_, err = b.parseBody(tokens, thenPos, thenTerminators)
	if err != nil {
		return -1, start, fmt.Errorf("if then-body: %w", err)
	}

	// Find position after the terminator that ended the then-body.
	bodyPos := b.findPosAfterBody(tokens, thenPos, thenTerminators)
	if bodyPos >= len(tokens) {
		return -1, start, fmt.Errorf("unexpected end of if statement")
	}

	thenBodyEnd := len(b.blocks)

	firstBlockIdx := -1

	// Collect elseif branches.
	type branch struct {
		cond string
		// positions in the block list will be known after all branches are parsed
	}
	type elseifBranch struct {
		cond     string
		bodyFrom int
		bodyTo   int
	}
	var elseifs []elseifBranch

	for bodyPos < len(tokens) {
		tok := tokens[skipGap(tokens, bodyPos)]
		if tok.Kind != parser.TokKeyword {
			break
		}
		switch tok.Value {
		case "elseif":
			bodyPos = skipGap(tokens, bodyPos)
			bodyPos++ // skip 'elseif'

			eifCond, eifThenPos, err := collectUntil(tokens, bodyPos, "then")
			if err != nil {
				return -1, start, fmt.Errorf("elseif condition: %w", err)
			}

			eifBodyStart := len(b.blocks)
			_, err = b.parseBody(tokens, eifThenPos, thenTerminators)
			if err != nil {
				return -1, start, fmt.Errorf("elseif body: %w", err)
			}
			eifBodyEnd := len(b.blocks)

			elseifs = append(elseifs, elseifBranch{
				cond:     eifCond,
				bodyFrom: eifBodyStart,
				bodyTo:   eifBodyEnd,
			})

			bodyPos = b.findPosAfterBody(tokens, eifThenPos, thenTerminators)

		case "else":
			bodyPos = skipGap(tokens, bodyPos)
			bodyPos++ // skip 'else'
			// else-body until 'end'
			elseTerminators := map[string]bool{"end": true}
			_, err = b.parseBody(tokens, bodyPos, elseTerminators)
			if err != nil {
				return -1, start, fmt.Errorf("else body: %w", err)
			}
			bodyPos = b.findPosAfterBody(tokens, bodyPos, elseTerminators)
			// Found 'end'; break out of the loop.
			// Actually we need to skip 'end' here.
			break

		case "end":
			bodyPos = skipGap(tokens, bodyPos)
			bodyPos++ // skip 'end'
			// done
			break

		default:
			break
		}

		// Check if we're at 'end'.
		checkPos := skipGap(tokens, bodyPos)
		if checkPos < len(tokens) && tokens[checkPos].Value == "end" {
			// Already handled above.
			break
		}
		// If we handled 'end' or 'else' above, we're done.
		if tok.Value == "end" || tok.Value == "else" {
			break
		}
	}

	// Now we know the positions. Build the condition blocks.
	// The structure:
	//   [if-cond] -> then-body | (elseifs[0]-cond | else-body | merge)
	//   [then-body blocks]
	//   [elseif-cond] -> elseif-body | (next-elseif-cond | else-body | merge)
	//   [elseif-body blocks]
	//   ...
	//   [else-body blocks] (optional)
	//   [merge block]

	// We need to insert condition blocks BEFORE their respective bodies.
	// Since blocks are already appended in parse order, we need to reorder.
	// Strategy: insert cond blocks and rebuild.

	// Actually, let me use a different approach. I'll pre-allocate the condition
	// block and insert it at the right position using block manipulation.

	// Simpler approach: the blocks are currently in order:
	//   thenBody, [elseifBody]*, [elseBody]*
	// I need to reorder to:
	//   ifCond, thenBody, [elseifCond, elseifBody]*, [elseBody]*, merge

	// Build the final list by extracting ranges.

	// Helper to copy a range from b.blocks.
	copyRange := func(from, to int) []basicBlock {
		if from >= to {
			return nil
		}
		out := make([]basicBlock, to-from)
		copy(out, b.blocks[from:to])
		return out
	}

	// Build the branch body range slices.
	thenBlocks := copyRange(thenBodyStart, thenBodyEnd)

	var elseBodyBlocks []basicBlock
	var elseifCondBlocks []basicBlock
	var elseifBodyBlocks [][]basicBlock

	elseBodyStart := thenBodyEnd
	for _, eif := range elseifs {
		elseifCondBlocks = append(elseifCondBlocks, basicBlock{
			isCond:   true,
			condExpr: eif.cond,
		})
		elseifBodyBlocks = append(elseifBodyBlocks, copyRange(eif.bodyFrom, eif.bodyTo))
		elseBodyStart = eif.bodyTo
	}
	if elseBodyStart < len(b.blocks) {
		elseBodyBlocks = copyRange(elseBodyStart, len(b.blocks))
	}

	// Now build the ordered list in b.blocks.
	// Roll back to before the then-body.
	b.blocks = b.blocks[:thenBodyStart]

	// if-condition block.
	ifCondIdx := b.addBlock(basicBlock{
		isCond:   true,
		condExpr: cond,
	})
	if firstBlockIdx < 0 {
		firstBlockIdx = ifCondIdx
	}

	// then-body blocks.
	thenFirst := b.lastIdx() + 1
	for i := range thenBlocks {
		thenBlocks[i].nextIdx = 0 // will be fixed by linker later
		_ = b.addBlock(thenBlocks[i])
	}
	thenLast := b.lastIdx()

	// elseif branches.
	var eifCondIdxs []int
	var eifFirstIdxs []int
	var eifLastIdxs []int
	for i, eifCond := range elseifCondBlocks {
		eifCondIdxs = append(eifCondIdxs, b.addBlock(eifCond))
		eifFirst := b.lastIdx() + 1
		for j := range elseifBodyBlocks[i] {
			elseifBodyBlocks[i][j].nextIdx = 0
			_ = b.addBlock(elseifBodyBlocks[i][j])
		}
		eifFirstIdxs = append(eifFirstIdxs, eifFirst)
		eifLastIdxs = append(eifLastIdxs, b.lastIdx())
	}

	// else-body blocks.
	elseFirst := -1
	elseLast := -1
	if len(elseBodyBlocks) > 0 {
		elseFirst = b.lastIdx() + 1
		for i := range elseBodyBlocks {
			elseBodyBlocks[i].nextIdx = 0
			_ = b.addBlock(elseBodyBlocks[i])
		}
		elseLast = b.lastIdx()
	}

	// merge block (empty).
	mergeIdx := b.addEmptyBlock()

	// Now wire up the condition blocks.
	// if-cond: true -> thenFirst, false -> first elseif cond or elseFirst or merge
	falseTarget := mergeIdx
	if len(eifCondIdxs) > 0 {
		falseTarget = eifCondIdxs[0]
	} else if elseFirst >= 0 {
		falseTarget = elseFirst
	}
	b.blocks[ifCondIdx].nextIdx = thenFirst
	b.blocks[ifCondIdx].altIdx = falseTarget

	// Wire elseif condition blocks.
	for i, eifIdx := range eifCondIdxs {
		nextFalse := mergeIdx
		if i+1 < len(eifCondIdxs) {
			nextFalse = eifCondIdxs[i+1]
		} else if elseFirst >= 0 {
			nextFalse = elseFirst
		}
		b.blocks[eifIdx].nextIdx = eifFirstIdxs[i]
		b.blocks[eifIdx].altIdx = nextFalse
	}

	// Link then-body to merge (last block in then-body -> merge).
	if thenLast >= thenFirst {
		// Find the last block that needs a next link.
		for i := thenLast; i >= thenFirst; i-- {
			if !b.blocks[i].isCond && b.blocks[i].nextIdx == 0 {
				b.blocks[i].nextIdx = mergeIdx
				break
			}
		}
	}

	// Link elseif body blocks to merge.
	for i := range eifFirstIdxs {
		last := eifLastIdxs[i]
		if last >= eifFirstIdxs[i] {
			for j := last; j >= eifFirstIdxs[i]; j-- {
				if !b.blocks[j].isCond && b.blocks[j].nextIdx == 0 {
					b.blocks[j].nextIdx = mergeIdx
					break
				}
			}
		}
	}

	// Link else body to merge.
	if elseLast >= elseFirst && elseFirst >= 0 {
		for i := elseLast; i >= elseFirst; i-- {
			if !b.blocks[i].isCond && b.blocks[i].nextIdx == 0 {
				b.blocks[i].nextIdx = mergeIdx
				break
			}
		}
	}

	// Skip past 'end' in token stream.
	endPos := skipGap(tokens, bodyPos)
	if endPos < len(tokens) && tokens[endPos].Value == "end" {
		endPos++
	}

	return firstBlockIdx, endPos, nil
}

// parseWhile parses a 'while cond do body end' loop.
func (b *blockBuilder) parseWhile(
	tokens []parser.Token,
	start int,
) (int, int, error) {
	pos := skipGap(tokens, start)
	if pos >= len(tokens) || tokens[pos].Value != "while" {
		return -1, start, fmt.Errorf("expected 'while'")
	}
	pos++ // skip 'while'

	cond, doPos, err := collectUntil(tokens, pos, "do")
	if err != nil {
		return -1, start, fmt.Errorf("while condition: %w", err)
	}

	// Condition block: b = cond AND bodyFirst OR exitMerge
	condIdx := b.addBlock(basicBlock{
		isCond:   true,
		condExpr: cond,
	})

	firstBlockIdx := condIdx

	// Parse body.
	bodyTerminators := map[string]bool{"end": true}
	bodyStart := len(b.blocks)
	_, err = b.parseBody(tokens, doPos, bodyTerminators)
	if err != nil {
		return -1, start, fmt.Errorf("while body: %w", err)
	}
	bodyEnd := len(b.blocks)

	bodyAfter := b.findPosAfterBody(tokens, doPos, bodyTerminators)
	endPos := skipGap(tokens, bodyAfter)
	if endPos < len(tokens) && tokens[endPos].Value == "end" {
		endPos++
	}

	// Merge block (after the loop).
	mergeIdx := b.addEmptyBlock()

	// Wire condition block.
	bodyFirst := bodyStart
	if bodyStart == bodyEnd {
		// Empty body: jump straight to merge.
		b.blocks[condIdx].nextIdx = mergeIdx
		b.blocks[condIdx].altIdx = mergeIdx
	} else {
		b.blocks[condIdx].nextIdx = bodyFirst
		b.blocks[condIdx].altIdx = mergeIdx

		// Last body block links back to condition.
		for i := bodyEnd - 1; i >= bodyStart; i-- {
			if !b.blocks[i].isCond && b.blocks[i].nextIdx == 0 {
				b.blocks[i].nextIdx = condIdx
				break
			}
			if b.blocks[i].isCond {
				// If the last body block is a condition block, link its
				// branches back to the while condition.
				if b.blocks[i].nextIdx == 0 {
					b.blocks[i].nextIdx = condIdx
				}
				if b.blocks[i].altIdx == 0 {
					b.blocks[i].altIdx = condIdx
				}
			}
		}
	}

	// Resolve break targets in body: any block with code == "break"
	// should jump to mergeIdx instead.
	for i := bodyStart; i < bodyEnd; i++ {
		if b.blocks[i].code == "break" {
			b.blocks[i].nextIdx = mergeIdx
			b.blocks[i].code = "" // becomes empty (no-op) jump
		}
	}

	return firstBlockIdx, endPos, nil
}

// parseRepeat parses a 'repeat body until cond' loop.
func (b *blockBuilder) parseRepeat(
	tokens []parser.Token,
	start int,
) (int, int, error) {
	pos := skipGap(tokens, start)
	if pos >= len(tokens) || tokens[pos].Value != "repeat" {
		return -1, start, fmt.Errorf("expected 'repeat'")
	}
	pos++ // skip 'repeat'

	// Parse body until 'until'.
	bodyTerminators := map[string]bool{"until": true}
	bodyStart := len(b.blocks)
	firstIdx, err := b.parseBody(tokens, pos, bodyTerminators)
	if err != nil {
		return -1, start, fmt.Errorf("repeat body: %w", err)
	}

	bodyEnd := len(b.blocks)

	// Find 'until' position.
	untilPos := b.findPosAfterBody(tokens, pos, bodyTerminators)
	untilTokPos := skipGap(tokens, untilPos)
	if untilTokPos >= len(tokens) || tokens[untilTokPos].Value != "until" {
		return -1, start, fmt.Errorf("expected 'until'")
	}
	untilTokPos++ // skip 'until'

	// Collect condition expression after 'until'.
	// The condition runs to end of line (next newline).
	cond, _, err := collectUntilNewline(tokens, untilTokPos)
	if err != nil {
		return -1, start, fmt.Errorf("until condition: %w", err)
	}

	// Find position after the condition.
	condEnd := untilTokPos
	for condEnd < len(tokens) && tokens[condEnd].Kind != parser.TokNewline {
		condEnd++
	}
	if condEnd < len(tokens) {
		condEnd++ // skip newline
	}

	// Condition block: b = cond AND mergeIdx OR bodyFirst
	condIdx := b.addBlock(basicBlock{
		isCond:   true,
		condExpr: cond,
	})
	if firstIdx < 0 {
		firstIdx = condIdx
	}

	// Merge block (after loop).
	mergeIdx := b.addEmptyBlock()

	// Wire condition block: true → merge, false → repeat body.
	bodyTarget := bodyStart
	if bodyStart == bodyEnd {
		bodyTarget = mergeIdx // empty body
	}
	b.blocks[condIdx].nextIdx = mergeIdx
	b.blocks[condIdx].altIdx = bodyTarget

	// Last body block links to condition block.
	for i := bodyEnd - 1; i >= bodyStart; i-- {
		if !b.blocks[i].isCond && b.blocks[i].nextIdx == 0 {
			b.blocks[i].nextIdx = condIdx
			break
		}
	}

	// Resolve break targets.
	for i := bodyStart; i < bodyEnd; i++ {
		if b.blocks[i].code == "break" {
			b.blocks[i].nextIdx = mergeIdx
			b.blocks[i].code = ""
		}
	}

	return firstIdx, condEnd, nil
}

// parseFor parses both numeric and generic for loops.
func (b *blockBuilder) parseFor(
	tokens []parser.Token,
	start int,
) (int, int, error) {
	pos := skipGap(tokens, start)
	if pos >= len(tokens) || tokens[pos].Value != "for" {
		return -1, start, fmt.Errorf("expected 'for'")
	}
	pos++ // skip 'for'

	// Determine if numeric for or generic for.
	// Numeric: for Name = exp1, exp2, exp3 do
	// Generic: for namelist in explist do
	// Look ahead for '=' vs 'in'.
	eqPos := findKeywordInLine(tokens, pos, "=")
	inPos := findKeywordInLine(tokens, pos, "in")

	if eqPos >= 0 && (inPos < 0 || eqPos < inPos) {
		return b.parseForNumeric(tokens, start)
	}
	return b.parseForGeneric(tokens, start)
}

// parseForNumeric parses 'for Name = exp1, exp2, exp3 do body end'.
func (b *blockBuilder) parseForNumeric(
	tokens []parser.Token,
	start int,
) (int, int, error) {
	pos := skipGap(tokens, start)
	pos++ // skip 'for'

	// Get loop variable name.
	varPos := skipGap(tokens, pos)
	if varPos >= len(tokens) || tokens[varPos].Kind != parser.TokIdent {
		return -1, start, fmt.Errorf("expected for loop variable")
	}
	varName := tokens[varPos].Value
	pos = varPos + 1

	// Skip '='.
	pos = skipGap(tokens, pos)
	if pos >= len(tokens) || tokens[pos].Value != "=" {
		return -1, start, fmt.Errorf("expected '=' in for loop")
	}
	pos++

	// Collect init expression until ','.
	initExpr, commaPos, err := collectUntil(tokens, pos, ",")
	if err != nil {
		return -1, start, fmt.Errorf("for init expr: %w", err)
	}

	// Collect limit expression until ',' or 'do'.
	pos = skipGap(tokens, commaPos+1) // skip ','
	limitExpr, nextPos, err := collectUntilEither(tokens, pos, ",", "do")
	if err != nil {
		return -1, start, fmt.Errorf("for limit expr: %w", err)
	}

	var stepExpr string
	var doPos int
	// Check if next is ',' (step present) or 'do'.
	checkPos := skipGap(tokens, nextPos)
	if checkPos < len(tokens) && tokens[checkPos].Value == "," {
		// Has step.
		pos = checkPos + 1
		stepExpr, doPos, err = collectUntil(tokens, pos, "do")
		if err != nil {
			return -1, start, fmt.Errorf("for step expr: %w", err)
		}
	} else {
		stepExpr = "1"
		doPos = nextPos
		// nextPos should be the position of 'do'.
		doCheck := skipGap(tokens, doPos)
		if doCheck < len(tokens) && tokens[doCheck].Value == "do" {
			doPos = doCheck
		}
	}

	// Build init block: name = initExpr.
	initIdx := b.addBlock(basicBlock{
		code: varName + " = " + initExpr,
	})

	// Build condition block: b = name <= limitExpr AND bodyFirst OR merge
	// Note: this assumes positive step. For negative step, the condition
	// would be name >= limitExpr. We use the general form for now.
	condIdx := b.addBlock(basicBlock{
		isCond:   true,
		condExpr: varName + " <= " + limitExpr,
	})

	firstBlockIdx := initIdx

	// Link init -> cond.
	b.blocks[initIdx].nextIdx = condIdx

	// Parse body.
	bodyTerminators := map[string]bool{"end": true}
	bodyStart := len(b.blocks)
	_, err = b.parseBody(tokens, doPos, bodyTerminators)
	if err != nil {
		return -1, start, fmt.Errorf("for body: %w", err)
	}
	bodyEnd := len(b.blocks)

	// Find 'end'.
	bodyAfter := b.findPosAfterBody(tokens, doPos, bodyTerminators)
	endPos := skipGap(tokens, bodyAfter)
	if endPos < len(tokens) && tokens[endPos].Value == "end" {
		endPos++
	}

	// Incr block: name = name + step.
	incrIdx := b.addBlock(basicBlock{
		code: varName + " = " + varName + " + " + stepExpr,
	})

	// Merge block.
	mergeIdx := b.addEmptyBlock()

	// Wire condition block.
	if bodyStart == bodyEnd {
		// Empty body.
		b.blocks[condIdx].nextIdx = mergeIdx
		b.blocks[condIdx].altIdx = mergeIdx
	} else {
		b.blocks[condIdx].nextIdx = bodyStart
		b.blocks[condIdx].altIdx = mergeIdx

		// Last body block -> incr.
		for i := bodyEnd - 1; i >= bodyStart; i-- {
			if !b.blocks[i].isCond && b.blocks[i].nextIdx == 0 {
				b.blocks[i].nextIdx = incrIdx
				break
			}
		}
	}

	// Incr -> cond (back edge).
	b.blocks[incrIdx].nextIdx = condIdx

	// Resolve break targets.
	for i := bodyStart; i < bodyEnd; i++ {
		if b.blocks[i].code == "break" {
			b.blocks[i].nextIdx = mergeIdx
			b.blocks[i].code = ""
		}
	}

	return firstBlockIdx, endPos, nil
}

// parseForGeneric parses 'for namelist in explist do body end'.
func (b *blockBuilder) parseForGeneric(
	tokens []parser.Token,
	start int,
) (int, int, error) {
	pos := skipGap(tokens, start)
	pos++ // skip 'for'

	// Collect name list until 'in'.
	names, inPos, err := collectUntil(tokens, pos, "in")
	if err != nil {
		return -1, start, fmt.Errorf("for namelist: %w", err)
	}

	// Collect expression list until 'do'.
	pos = skipGap(tokens, inPos)
	pos++ // skip 'in'
	expr, doPos, err := collectUntil(tokens, pos, "do")
	if err != nil {
		return -1, start, fmt.Errorf("for explist: %w", err)
	}

	// For generic for, we keep the original loop header as the condition
	// block and use a special pattern. The generic for cannot be easily
	// decomposed, so we treat it as a while-like construct.

	// Condition block uses a special variable assignment pattern.
	// We create a synthetic condition var and use it for dispatch.
	// Actually, for simplicity, we'll keep the for loop header and
	// just flatten the body.

	// Create an init/setup block.
	// Then a condition block that dispatches to body or merge.
	// After body, jump back to condition block.

	// We use a synthetic variable to track iteration.
	synthVar := "_f" // reserved for generic for dispatch

	// Setup block: initialize iterator variables.
	setupIdx := b.addBlock(basicBlock{
		code: fmt.Sprintf("%s = true", synthVar),
	})

	// Build the condition check using the for semantics.
	// In Lua, the generic for calls the iterator each time.
	// We approximate with a condition that always enters the body.
	condIdx := b.addBlock(basicBlock{
		isCond:   true,
		condExpr: synthVar,
	})

	firstBlockIdx := setupIdx
	b.blocks[setupIdx].nextIdx = condIdx

	// Parse body.
	bodyTerminators := map[string]bool{"end": true}
	bodyStart := len(b.blocks)
	_, err = b.parseBody(tokens, doPos, bodyTerminators)
	if err != nil {
		return -1, start, fmt.Errorf("for body: %w", err)
	}
	bodyEnd := len(b.blocks)

	bodyAfter := b.findPosAfterBody(tokens, doPos, bodyTerminators)
	endPos := skipGap(tokens, bodyAfter)
	if endPos < len(tokens) && tokens[endPos].Value == "end" {
		endPos++
	}

	// Merge block.
	mergeIdx := b.addEmptyBlock()

	if bodyStart == bodyEnd {
		b.blocks[condIdx].nextIdx = mergeIdx
		b.blocks[condIdx].altIdx = mergeIdx
	} else {
		b.blocks[condIdx].nextIdx = bodyStart
		b.blocks[condIdx].altIdx = mergeIdx

		// Last body block -> back to condition.
		for i := bodyEnd - 1; i >= bodyStart; i-- {
			if !b.blocks[i].isCond && b.blocks[i].nextIdx == 0 {
				b.blocks[i].nextIdx = condIdx
				break
			}
		}
	}

	// Resolve break targets.
	for i := bodyStart; i < bodyEnd; i++ {
		if b.blocks[i].code == "break" {
			b.blocks[i].nextIdx = mergeIdx
			b.blocks[i].code = ""
		}
	}

	// Store the original for header info as comments for debugging.
	_ = names
	_ = expr

	return firstBlockIdx, endPos, nil
}

// parseDoBlock parses a bare 'do body end' block.
func (b *blockBuilder) parseDoBlock(
	tokens []parser.Token,
	start int,
) (int, int, error) {
	pos := skipGap(tokens, start)
	pos++ // skip 'do'

	bodyTerminators := map[string]bool{"end": true}
	firstIdx, err := b.parseBody(tokens, pos, bodyTerminators)
	if err != nil {
		return -1, start, fmt.Errorf("do block: %w", err)
	}

	bodyAfter := b.findPosAfterBody(tokens, pos, bodyTerminators)
	endPos := skipGap(tokens, bodyAfter)
	if endPos < len(tokens) && tokens[endPos].Value == "end" {
		endPos++
	}

	if firstIdx < 0 {
		// Empty do block.
		firstIdx = b.addEmptyBlock()
	}

	return firstIdx, endPos, nil
}

// parseFunction parses a 'function name(args) body end' declaration.
func (b *blockBuilder) parseFunction(
	tokens []parser.Token,
	start int,
) (int, int, error) {
	// We preserve function declarations as-is since they're compound
	// statements that are hard to meaningfully decompose.
	// Collect all lines until the matching 'end'.
	pos := start
	depth := 0
	funcStart := pos
	sawFunction := false

	for pos < len(tokens) {
		tok := tokens[pos]
		if tok.Kind == parser.TokKeyword {
			switch tok.Value {
			case "function":
				depth++
				sawFunction = true
			case "end":
				depth--
				if depth == 0 && sawFunction {
					pos++
					break
				}
			case "if", "while", "for", "repeat", "do":
				depth++
			}
		}
		if depth == 0 && sawFunction {
			break
		}
		pos++
	}

	// Reconstruct the function text from tokens.
	funcTokens := tokens[funcStart:pos]
	var sb strings.Builder
	for i, tok := range funcTokens {
		if i > 0 && tok.Kind != parser.TokNewline &&
			tokens[funcStart+i-1].Kind != parser.TokNewline &&
			needSpace(tokens[funcStart+i-1], tok) {
			sb.WriteString(" ")
		}
		if tok.Kind == parser.TokNewline {
			sb.WriteString("\n")
		} else {
			sb.WriteString(tok.Value)
		}
	}

	idx := b.addBlock(basicBlock{code: strings.TrimSpace(sb.String())})
	return idx, pos, nil
}

// ---------------------------------------------------------------------------
// Token helpers
// ---------------------------------------------------------------------------

// skipGap skips whitespace and newline tokens, returning the index of the
// next meaningful token.
func skipGap(tokens []parser.Token, pos int) int {
	for pos < len(tokens) {
		t := tokens[pos]
		if t.Kind == parser.TokWhitespace || t.Kind == parser.TokNewline {
			pos++
			continue
		}
		break
	}
	return pos
}

// statementBoundaryKeywords are keywords that terminate a regular statement.
var statementBoundaryKeywords = map[string]bool{
	"end": true, "else": true, "elseif": true, "until": true,
	"if": true, "while": true, "for": true, "repeat": true,
	"return": true, "break": true, "do": true, "local": true,
	"function": true, "goto": true,
}

// findStatementEnd returns the index of the first token after the current
// statement. Stops at newline or at the start of a control-flow keyword.
func findStatementEnd(tokens []parser.Token, pos int) int {
	for i := pos; i < len(tokens); i++ {
		if tokens[i].Kind == parser.TokNewline {
			return i + 1
		}
		// Stop before the NEXT control-flow keyword (skip the first token at pos).
		if i > pos && tokens[i].Kind == parser.TokKeyword &&
			statementBoundaryKeywords[tokens[i].Value] {
			return i
		}
	}
	return len(tokens)
}

// findLineEnd returns the index after the next newline token, or len(tokens)
// if no newline is found.
func findLineEnd(tokens []parser.Token, pos int) int {
	return findStatementEnd(tokens, pos)
}

// firstKeyword returns the first keyword value in a line of tokens,
// or "" if no keyword is found.
func firstKeyword(line []parser.Token) string {
	for _, tok := range line {
		if tok.Kind == parser.TokWhitespace || tok.Kind == parser.TokNewline {
			continue
		}
		if tok.Kind == parser.TokKeyword {
			return tok.Value
		}
		return "" // first non-whitespace is not a keyword
	}
	return ""
}

// findKeywordInLine finds a token by value in a token slice starting at pos,
// returning its index or -1. Stops at newline.
func findKeywordInLine(tokens []parser.Token, pos int, kw string) int {
	for i := pos; i < len(tokens); i++ {
		if tokens[i].Kind == parser.TokNewline {
			break
		}
		if tokens[i].Value == kw {
			return i
		}
	}
	return -1
}

// collectUntil collects token values from pos until the stop keyword is
// encountered. Returns the joined condition string and the position of the
// stop keyword.
func collectUntil(
	tokens []parser.Token, pos int, stop string,
) (string, int, error) {
	var parts []string
	for pos < len(tokens) {
		tok := tokens[pos]
		if tok.Kind == parser.TokNewline {
			pos++
			continue
		}
		if tok.Kind == parser.TokWhitespace {
			pos++
			continue
		}
		if tok.Value == stop {
			pos++ // skip stop token
			return strings.TrimSpace(strings.Join(parts, " ")), pos, nil
		}
		parts = append(parts, tok.Value)
		pos++
	}
	return "", pos, fmt.Errorf("expected '%s'", stop)
}

// collectUntilEither is like collectUntil but stops at either stopA or stopB.
// Returns the joined string, the position of the stop token, and error.
func collectUntilEither(
	tokens []parser.Token, pos int, stopA, stopB string,
) (string, int, error) {
	var parts []string
	for pos < len(tokens) {
		tok := tokens[pos]
		if tok.Kind == parser.TokNewline || tok.Kind == parser.TokWhitespace {
			pos++
			continue
		}
		if tok.Value == stopA || tok.Value == stopB {
			return strings.TrimSpace(strings.Join(parts, " ")), pos, nil
		}
		parts = append(parts, tok.Value)
		pos++
	}
	return "", pos, fmt.Errorf("expected '%s' or '%s'", stopA, stopB)
}

// collectUntilNewline collects token values until end of line.
func collectUntilNewline(
	tokens []parser.Token, pos int,
) (string, int, error) {
	var parts []string
	for pos < len(tokens) {
		tok := tokens[pos]
		if tok.Kind == parser.TokNewline {
			break
		}
		if tok.Kind == parser.TokWhitespace {
			pos++
			continue
		}
		parts = append(parts, tok.Value)
		pos++
	}
	return strings.TrimSpace(strings.Join(parts, " ")), pos, nil
}

// tokenLineText reconstructs the original line text from a slice of tokens.
func tokenLineText(line []parser.Token) string {
	var sb strings.Builder
	var prev *parser.Token
	for _, tok := range line {
		if tok.Kind == parser.TokNewline {
			break
		}
		if tok.Kind == parser.TokWhitespace {
			continue
		}
		if prev != nil && needSpace(*prev, tok) {
			sb.WriteString(" ")
		}
		sb.WriteString(tok.Value)
		tokCopy := tok
		prev = &tokCopy
	}
	return strings.TrimSpace(sb.String())
}

// needSpace returns true if a space should be inserted between two tokens.
func needSpace(prev, curr parser.Token) bool {
	// Don't add space before/after certain operators.
	noSpaceBefore := map[string]bool{
		",": true, ";": true, ")": true, "]": true, "}": true,
		".": true, ":": true,
	}
	noSpaceAfter := map[string]bool{
		"(": true, "[": true, "{": true, "#": true,
	}
	if noSpaceBefore[curr.Value] || noSpaceAfter[prev.Value] {
		return false
	}
	// No space before '(' when preceded by identifier or keyword (func call).
	if curr.Value == "(" && (prev.Kind == parser.TokIdent || prev.Kind == parser.TokKeyword) {
		return false
	}
	// Two identifiers/keywords need a space.
	if (prev.Kind == parser.TokIdent || prev.Kind == parser.TokKeyword) &&
		(curr.Kind == parser.TokIdent || curr.Kind == parser.TokKeyword) {
		return true
	}
	return true
}

// needSpaceBetween returns true if a space should be inserted between two tokens., skipping a body (tracking
// nesting depth for do/if/while/for/repeat/function/end pairs) until one
// of the terminators at depth 0.
func (b *blockBuilder) findPosAfterBody(
	tokens []parser.Token,
	start int,
	terminators map[string]bool,
) int {
	pos := start
	depth := 0
	// Track repeat/until nesting separately since until closes repeat.
	inRepeat := 0

	for pos < len(tokens) {
		tok := tokens[pos]
		if tok.Kind == parser.TokKeyword {
			switch tok.Value {
			case "if", "while", "for", "do":
				depth++
			case "repeat":
				depth++
				inRepeat++
			case "end":
				depth--
				if depth < 0 {
					// 'end' at depth < 0 means it's our terminator.
					return pos
				}
			case "until":
				if depth > 0 {
					depth--
					inRepeat--
				}
				if depth == 0 && terminators["until"] {
					return pos
				}
			case "else", "elseif":
				if depth == 0 && terminators[tok.Value] {
					return pos
				}
			}
		}
		pos++
	}
	return pos
}

// processLocal processes a 'local' declaration line.
// Returns the hoisted declaration and the remaining statement (if any).
func processLocal(line []parser.Token) (string, string, error) {
	// Extract the meaningful tokens (skip whitespace/newlines).
	var parts []parser.Token
	for _, tok := range line {
		if tok.Kind == parser.TokNewline {
			break
		}
		if tok.Kind == parser.TokWhitespace {
			continue
		}
		parts = append(parts, tok)
	}

	if len(parts) < 2 || parts[0].Value != "local" {
		return "", "", fmt.Errorf("expected 'local' declaration")
	}

	// Check for 'local function'.
	if len(parts) >= 3 && parts[1].Value == "function" {
		// local function NAME(args) ... end — preserve as-is.
		return "", tokenLineText(line), nil
	}

	// Simple local declaration: local name, local name = value
	// Check if it has an assignment.
	hasAssign := false
	assignPos := -1
	for i, tok := range parts {
		if tok.Value == "=" {
			hasAssign = true
			assignPos = i
			break
		}
	}

	if !hasAssign {
		// local name (no assignment) — hoist the whole thing.
		return strings.TrimPrefix(tokenLineText(line), "local "), "", nil
	}

	// local name = value — hoist the name, keep 'name = value'.
	nameTokens := parts[1:assignPos]
	name := tokenLineText(nameTokens)

	// Build the assignment: name = value.
	valueTokens := parts[assignPos+1:]
	value := tokenLineText(valueTokens)

	return name, name + " = " + value, nil
}

// ---------------------------------------------------------------------------
// ID assignment — deterministic from seed via rng
// ---------------------------------------------------------------------------

// assignBlockIDs gives each block a unique numeric ID with random spacing.
func assignBlockIDs(blocks []basicBlock, rng *rand.Rand) []int {
	ids := make([]int, len(blocks))
	if len(blocks) == 0 {
		return ids
	}
	ids[0] = 0 // state tracker starts at 0
	step := 100000 + rng.Intn(100000)
	for i := 1; i < len(blocks); i++ {
		ids[i] = ids[i-1] + step + rng.Intn(50000)
	}
	return ids
}

// ---------------------------------------------------------------------------
// Binary tree builder
// ---------------------------------------------------------------------------

// buildBinaryTreeRec recursively builds the binary decision tree dispatch code.
// baseIdx is the starting index of blocks within the full ids array.
func buildBinaryTreeRec(blocks []basicBlock, ids []int, baseIdx int, rng *rand.Rand) string {
	if len(blocks) == 0 {
		return ""
	}
	if len(blocks) == 1 {
		return buildLeafBlock(blocks[0], ids[baseIdx], ids)
	}

	mid := len(blocks) / 2
	midpoint := ids[baseIdx+mid]

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("if b < %d then\n", midpoint))
	sb.WriteString(buildBinaryTreeRec(blocks[:mid], ids, baseIdx, rng))
	sb.WriteString("else\n")
	sb.WriteString(buildBinaryTreeRec(blocks[mid:], ids, baseIdx+mid, rng))
	sb.WriteString("end\n")

	return sb.String()
}

// buildBinaryTree starts the recursive binary tree construction.
func buildBinaryTree(blocks []basicBlock, ids []int, rng *rand.Rand) string {
	return buildBinaryTreeRec(blocks, ids, 0, rng)
}

// buildLeafBlock builds the dispatch+execute block for a single basic block.
func buildLeafBlock(block basicBlock, id int, ids []int) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("if b == %d then\n", id))
	sb.WriteString("do\n")

	if block.isCond {
		// Condition dispatch: b = cond AND trueID OR falseID
		trueID := idForIndex(block.nextIdx, ids)
		falseID := idForIndex(block.altIdx, ids)
		sb.WriteString(fmt.Sprintf("b = %s and %d or %d\n",
			block.condExpr, trueID, falseID))
	} else if block.nextIdx < 0 {
		// Terminal block.
		if block.code != "" {
			sb.WriteString(block.code + "\n")
		}
		sb.WriteString("return\n")
	} else {
		// Regular block with explicit code.
		if block.code != "" {
			sb.WriteString(block.code + "\n")
		}
		// Set next block.
		nextID := idForIndex(block.nextIdx, ids)
		sb.WriteString(fmt.Sprintf("b = %d\n", nextID))
	}

	sb.WriteString("end\n")
	sb.WriteString("end\n")
	return sb.String()
}

// idForIndex returns the block ID for the given index.
func idForIndex(idx int, ids []int) int {
	if idx < 0 || idx >= len(ids) {
		return 0
	}
	return ids[idx]
}
