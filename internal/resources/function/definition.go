package function

import (
	"errors"
	"fmt"
	"strings"

	"github.com/caberdo/terraform-provider-trino/internal/resourceutil"
)

// A routine definition is user-authored SQL, so the provider cannot quote it
// like a value. It can still compare definitions token by token: keyword and
// unquoted-identifier case, whitespace and comments do not matter, while
// string literals and quoted identifiers do.

// canonicalDefinition renders a definition as a comparison key. Two
// definitions are equivalent when their keys are equal.
func canonicalDefinition(definition string) (string, error) {
	tokens, err := resourceutil.TokenizeSQL(resourceutil.TrimStatement(definition))
	if err != nil {
		return "", err
	}
	tokens = stripReturnParentheses(tokens)

	parts := make([]string, 0, len(tokens))
	for _, t := range tokens {
		switch t.Kind {
		case resourceutil.SQLWord:
			parts = append(parts, "w:"+strings.ToLower(t.Text))
		case resourceutil.SQLQuotedIdentifier:
			parts = append(parts, "i:"+t.Text)
		case resourceutil.SQLString:
			parts = append(parts, "s:"+t.Text)
		default:
			parts = append(parts, "p:"+t.Text)
		}
	}
	return strings.Join(parts, "\x1f"), nil
}

// stripReturnParentheses drops parentheses that wrap an entire RETURN
// expression. Trino's SHOW CREATE FUNCTION adds them around compound
// expressions, so `RETURN x + 1` and the stored `RETURN (x + 1)` compare equal
// while `RETURN (x + 1) * 2` keeps its meaningful grouping.
func stripReturnParentheses(tokens []resourceutil.SQLToken) []resourceutil.SQLToken {
	for i := 0; i < len(tokens); i++ {
		if !tokens[i].IsWord("return") {
			continue
		}
		for {
			end := returnExpressionEnd(tokens, i+1)
			if end <= i+1 || !tokens[i+1].IsSymbol("(") {
				break
			}
			close, err := resourceutil.MatchingParen(tokens, i+1)
			if err != nil || close != end-1 {
				break
			}
			updated := make([]resourceutil.SQLToken, 0, len(tokens)-2)
			updated = append(updated, tokens[:i+1]...)
			updated = append(updated, tokens[i+2:close]...)
			updated = append(updated, tokens[close+1:]...)
			tokens = updated
		}
	}
	return tokens
}

// returnExpressionEnd returns the exclusive end index of the expression that
// follows index start, stopping at a top-level semicolon.
func returnExpressionEnd(tokens []resourceutil.SQLToken, start int) int {
	depth := 0
	for i := start; i < len(tokens); i++ {
		switch {
		case tokens[i].IsSymbol("("):
			depth++
		case tokens[i].IsSymbol(")"):
			if depth == 0 {
				return i
			}
			depth--
		case tokens[i].IsSymbol(";") && depth == 0:
			return i
		}
	}
	return len(tokens)
}

// definitionsEquivalent reports whether two routine definitions are the same
// after normalising case, whitespace, comments and the parentheses that
// SHOW CREATE FUNCTION adds around RETURN expressions.
func definitionsEquivalent(a, b string) bool {
	left, err := canonicalDefinition(a)
	if err != nil {
		return false
	}
	right, err := canonicalDefinition(b)
	if err != nil {
		return false
	}
	return left == right
}

// parseSignature returns the parameter types of a routine definition, which
// DROP FUNCTION requires even though CREATE FUNCTION may use names.
func parseSignature(definition string) ([]string, error) {
	input := resourceutil.TrimStatement(definition)
	tokens, err := resourceutil.TokenizeSQL(input)
	if err != nil {
		return nil, err
	}
	if len(tokens) == 0 || !tokens[0].IsSymbol("(") {
		return nil, errors.New("the definition must start with a parameter list, for example (x bigint)")
	}
	close, err := resourceutil.MatchingParen(tokens, 0)
	if err != nil {
		return nil, err
	}

	parameterRanges := make([][2]int, 0, 2)
	start := 1
	depth := 0
	for i := 1; i < close; i++ {
		switch {
		case tokens[i].IsSymbol("("):
			depth++
		case tokens[i].IsSymbol(")"):
			depth--
		case tokens[i].IsSymbol(",") && depth == 0:
			parameterRanges = append(parameterRanges, [2]int{start, i})
			start = i + 1
		}
	}
	if start < close {
		parameterRanges = append(parameterRanges, [2]int{start, close})
	} else if len(parameterRanges) > 0 {
		return nil, errors.New("trailing comma in the parameter list")
	}

	parameterTypes := make([]string, 0, len(parameterRanges))
	for _, bounds := range parameterRanges {
		if bounds[1] <= bounds[0] {
			return nil, errors.New("empty parameter in the parameter list")
		}
		name := tokens[bounds[0]]
		if name.Kind != resourceutil.SQLWord && name.Kind != resourceutil.SQLQuotedIdentifier {
			return nil, fmt.Errorf("parameter %q has no name", name.Text)
		}
		if bounds[1]-bounds[0] < 2 {
			return nil, fmt.Errorf("parameter %q has no data type", name.Text)
		}
		typeStart := tokens[bounds[0]+1].Start
		typeEnd := tokens[bounds[1]-1].End
		parameterType := strings.TrimSpace(input[typeStart:typeEnd])
		if parameterType == "" {
			return nil, fmt.Errorf("parameter %q has no data type", name.Text)
		}
		parameterTypes = append(parameterTypes, parameterType)
	}

	returns := false
	for i := close + 1; i < len(tokens); i++ {
		if tokens[i].IsWord("returns") {
			returns = true
			break
		}
	}
	if !returns {
		return nil, errors.New("the definition must include RETURNS <type>")
	}

	return parameterTypes, nil
}

// canonicalArgumentTypes normalises the comma-separated argument types that
// SHOW FUNCTIONS returns so they can be compared with a parsed signature.
func canonicalArgumentTypes(raw string) string {
	types := splitTopLevel(raw)
	return canonicalTypeList(types)
}

func canonicalTypeList(types []string) string {
	parts := make([]string, len(types))
	for i, parameterType := range types {
		parts[i] = canonicalType(parameterType)
	}
	return strings.Join(parts, "\x1f")
}

func canonicalType(parameterType string) string {
	tokens, err := resourceutil.TokenizeSQL(parameterType)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(parameterType))
	}
	parts := make([]string, 0, len(tokens))
	for _, t := range tokens {
		switch t.Kind {
		case resourceutil.SQLWord:
			parts = append(parts, strings.ToLower(t.Text))
		case resourceutil.SQLQuotedIdentifier:
			parts = append(parts, t.Text)
		case resourceutil.SQLString:
			parts = append(parts, "'"+t.Text+"'")
		default:
			parts = append(parts, t.Text)
		}
	}
	return strings.Join(parts, "\x1f")
}

func splitTopLevel(input string) []string {
	tokens, err := resourceutil.TokenizeSQL(input)
	if err != nil {
		return []string{strings.TrimSpace(input)}
	}
	return splitRanges(input, tokens)
}

func splitRanges(input string, tokens []resourceutil.SQLToken) []string {
	if len(tokens) == 0 {
		return nil
	}
	var parts []string
	depth := 0
	start := tokens[0].Start
	for i, t := range tokens {
		switch {
		case t.IsSymbol("("):
			depth++
		case t.IsSymbol(")"):
			depth--
		case t.IsSymbol(",") && depth == 0:
			parts = append(parts, strings.TrimSpace(input[start:t.Start]))
			if i+1 < len(tokens) {
				start = tokens[i+1].Start
			}
		}
	}
	parts = append(parts, strings.TrimSpace(input[start:]))
	return parts
}

// extractDefinition removes the leading CREATE FUNCTION <qualified name> from
// the single-column text SHOW CREATE FUNCTION returns.
func extractDefinition(showCreate string) (string, error) {
	tokens, err := resourceutil.TokenizeSQL(showCreate)
	if err != nil {
		return "", err
	}
	if len(tokens) < 3 || !tokens[0].IsWord("create") || !tokens[1].IsWord("function") {
		return "", errors.New("SHOW CREATE FUNCTION did not return a CREATE FUNCTION statement")
	}
	position := 2
	if tokens[position].Kind != resourceutil.SQLWord && tokens[position].Kind != resourceutil.SQLQuotedIdentifier {
		return "", errors.New("SHOW CREATE FUNCTION returned an unexpected function name")
	}
	lastNameToken := position
	position++
	for position+1 < len(tokens) && tokens[position].IsSymbol(".") {
		if tokens[position+1].Kind != resourceutil.SQLWord && tokens[position+1].Kind != resourceutil.SQLQuotedIdentifier {
			return "", errors.New("SHOW CREATE FUNCTION returned an unexpected qualified name")
		}
		lastNameToken = position + 1
		position += 2
	}

	definition := strings.TrimSpace(showCreate[tokens[lastNameToken].End:])
	if !strings.HasPrefix(definition, "(") {
		return "", errors.New("SHOW CREATE FUNCTION returned a definition without a parameter list")
	}
	return definition, nil
}

// validateDefinition checks the parts the provider itself needs: a parameter
// list for DROP FUNCTION and a RETURNS clause for CREATE FUNCTION. Trino
// remains the authority on the rest of the routine.
func validateDefinition(definition string) error {
	if resourceutil.TrimStatement(definition) == "" {
		return errors.New("definition must not be empty")
	}
	_, err := parseSignature(definition)
	return err
}
