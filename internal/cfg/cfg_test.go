package cfg

import (
	"strings"
	"testing"
)

func TestFlattenWrapsInWhileLoop(t *testing.T) {
	input := `print("hello")`
	output, err := Flatten(input, 42)
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if !strings.Contains(output, "while b do") {
		t.Error("expected 'while b do' loop")
	}
	if !strings.Contains(output, "local b = 0") {
		t.Error("expected state tracker 'local b = 0'")
	}
}

func TestFlattenHasDoEndBlocks(t *testing.T) {
	input := `print("hello")`
	output, err := Flatten(input, 42)
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	// Each block should be wrapped in do...end.
	if strings.Count(output, "do") < 2 {
		t.Error("expected do...end blocks in CFG output")
	}
}

func TestFlattenPreservesPrint(t *testing.T) {
	input := `print("hello")`
	output, err := Flatten(input, 42)
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if !strings.Contains(output, `print`) {
		t.Error("expected print call preserved in CFG blocks")
	}
}

func TestFlattenDeterminism(t *testing.T) {
	a, _ := Flatten(`print("test")`, 42)
	b, _ := Flatten(`print("test")`, 42)
	if a != b {
		t.Error("same seed should produce identical CFG")
	}
}

func TestFlattenHoistsLocals(t *testing.T) {
	input := "local x = 5\nprint(x)"
	output, err := Flatten(input, 42)
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	// Local should be hoisted before while b do.
	beforeWhile := output[:strings.Index(output, "while b do")]
	if !strings.Contains(beforeWhile, "local x") {
		t.Error("expected 'local x' hoisted before while loop")
	}
}

func TestFlattenMultipleStatements(t *testing.T) {
	input := "local a = 1\nlocal b = 2\nprint(a + b)"
	output, err := Flatten(input, 42)
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if !strings.Contains(output, "while b do") {
		t.Error("expected CFG wrapper")
	}
}

func TestFlattenConditional(t *testing.T) {
	input := "if true then print(\"yes\") end"
	output, err := Flatten(input, 42)
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if !strings.Contains(output, "while b do") {
		t.Error("conditional should be flattened")
	}
}

func TestFlattenConditionalDispatch(t *testing.T) {
	input := "if x > 5 then print(\"big\") else print(\"small\") end"
	output, err := Flatten(input, 42)
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	// Should use b = cond and X or Y dispatch pattern.
	if !strings.Contains(output, " and ") || !strings.Contains(output, " or ") {
		t.Error("expected b = condition and X or Y dispatch")
	}
	if !strings.Contains(output, "x > 5") {
		t.Error("expected condition expression preserved")
	}
}

func TestFlattenWhileLoop(t *testing.T) {
	input := "while x < 10 do\nx = x + 1\nend"
	output, err := Flatten(input, 42)
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if !strings.Contains(output, "while b do") {
		t.Error("while loop should be flattened into while-b-do")
	}
	if !strings.Contains(output, "x < 10") {
		t.Error("while condition should be preserved")
	}
}

func TestFlattenRepeatLoop(t *testing.T) {
	input := "repeat\nx = x + 1\nuntil x > 10"
	output, err := Flatten(input, 42)
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if !strings.Contains(output, "while b do") {
		t.Error("repeat loop should be flattened")
	}
	if !strings.Contains(output, "x > 10") {
		t.Error("until condition should be preserved")
	}
}

func TestFlattenForNumeric(t *testing.T) {
	input := "for i = 1, 10 do\nprint(i)\nend"
	output, err := Flatten(input, 42)
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if !strings.Contains(output, "while b do") {
		t.Error("for loop should be flattened")
	}
	if !strings.Contains(output, "i = 1") {
		t.Error("for init should assign loop variable")
	}
}

func TestFlattenNestedIf(t *testing.T) {
	input := "if a then\nif b then\nprint(\"both\")\nend\nend"
	output, err := Flatten(input, 42)
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if !strings.Contains(output, "while b do") {
		t.Error("nested if should be flattened")
	}
}

func TestFlattenReturn(t *testing.T) {
	input := "return 42"
	output, err := Flatten(input, 42)
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if !strings.Contains(output, "while b do") {
		t.Error("return should be in while-b-do")
	}
	if !strings.Contains(output, "return") {
		t.Error("return should be preserved")
	}
}

func TestFlattenIfElseifElse(t *testing.T) {
	input := "if a == 1 then\nprint(\"1\")\nelseif a == 2 then\nprint(\"2\")\nelse\nprint(\"other\")\nend"
	output, err := Flatten(input, 42)
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if !strings.Contains(output, "while b do") {
		t.Error("if/elseif/else should be flattened")
	}
}

func TestFlattenEmptyInput(t *testing.T) {
	output, err := Flatten("", 42)
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if !strings.Contains(output, "while b do") {
		t.Error("empty input should produce valid CFG")
	}
}

func TestFlattenVariableSpacing(t *testing.T) {
	// Verify no extraneous spaces in function calls.
	input := `print("hello")`
	output, err := Flatten(input, 42)
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if strings.Contains(output, "print (") {
		t.Error("should not have space between function name and '('")
	}
}
