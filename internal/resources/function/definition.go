package function

import (
	"errors"
	"fmt"
	"strings"
)

// A routine definition is user-authored SQL, so the provider cannot quote it
// like a value. It can still compare definitions token by token: keyword and
// unquoted-identifier case, whitespace and comments do not matter, while
// string literals and quoted identifiers do.

type tokenKind int

const (
	tokenWord tokenKind = iota
	tokenQuotedIdentifier
	tokenString
	tokenSymbol
)

type token struct {
	kind  tokenKind
	text  string
	start int
	end   int
}

func (t token) isSymbol(symbol string) bool {
	return t.kind == tokenSymbol && t.text == symbol
}

func (t token) isWord(word string) bool {
	return t.kind == tokenWord && strings.EqualFold(t.text, word)
}

func isSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
}

func isSymbolByte(c byte) bool {
	switch c {
	case '(', ')', ',', ';', '.', '+', '-', '*', '/', '%', '<', '>', '=', '!',
		'|', '&', '^', '~', ':', '[', ']', '{', '}', '?':
		return true
	default:
		return false
	}
}

// tokenize splits a routine definition into words, quoted identifiers, string
// literals and single-byte symbols, skipping whitespace and SQL comments. It
// records byte offsets so callers can recover the original text of a range.
func tokenize(input string) ([]token, error) {
	tokens := make([]token, 0, 32)
	for i := 0; i < len(input); {
		c := input[i]
		switch {
		case isSpaceByte(c):
			i++
		case c == '-' && i+1 < len(input) && input[i+1] == '-':
			for i < len(input) && input[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(input) && input[i+1] == '*':
			end := strings.Index(input[i+2:], "*/")
			if end < 0 {
				return nil, errors.New("unterminated block comment")
			}
			i += 2 + end + 2
		case c == '\'':
			value, next, err := readQuoted(input, i, '\'')
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, token{kind: tokenString, text: value, start: i, end: next})
			i = next
		case c == '"':
			value, next, err := readQuoted(input, i, '"')
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, token{kind: tokenQuotedIdentifier, text: value, start: i, end: next})
			i = next
		case isSymbolByte(c):
			tokens = append(tokens, token{kind: tokenSymbol, text: input[i : i+1], start: i, end: i + 1})
			i++
		default:
			start := i
			for i < len(input) && !isSpaceByte(input[i]) && !isSymbolByte(input[i]) &&
				input[i] != '\'' && input[i] != '"' {
				i++
			}
			tokens = append(tokens, token{kind: tokenWord, text: input[start:i], start: start, end: i})
		}
	}
	return tokens, nil
}

func readQuoted(input string, start int, quote byte) (string, int, error) {
	var b strings.Builder
	i := start + 1
	for i < len(input) {
		if input[i] == quote {
			if i+1 < len(input) && input[i+1] == quote {
				b.WriteByte(quote)
				i += 2
				continue
			}
			return b.String(), i + 1, nil
		}
		b.WriteByte(input[i])
		i++
	}
	return "", 0, fmt.Errorf("unterminated quoted value starting at byte %d", start)
}

// matchingParen returns the index of the closing parenthesis that pairs with
// the opening parenthesis at open.
func matchingParen(tokens []token, open int) (int, error) {
	if open >= len(tokens) || !tokens[open].isSymbol("(") {
		return 0, errors.New("expected an opening parenthesis")
	}
	depth := 0
	for i := open; i < len(tokens); i++ {
		switch {
		case tokens[i].isSymbol("("):
			depth++
		case tokens[i].isSymbol(")"):
			depth--
			if depth == 0 {
				return i, nil
			}
		}
	}
	return 0, errors.New("unbalanced parentheses")
}

// canonicalDefinition renders a definition as a comparison key. Two
// definitions are equivalent when their keys are equal.
func canonicalDefinition(definition string) (string, error) {
	tokens, err := tokenize(trimStatement(definition))
	if err != nil {
		return "", err
	}
	tokens = stripReturnParentheses(tokens)

	parts := make([]string, 0, len(tokens))
	for _, t := range tokens {
		switch t.kind {
		case tokenWord:
			parts = append(parts, "w:"+strings.ToLower(t.text))
		case tokenQuotedIdentifier:
			parts = append(parts, "i:"+t.text)
		case tokenString:
			parts = append(parts, "s:"+t.text)
		default:
			parts = append(parts, "p:"+t.text)
		}
	}
	return strings.Join(parts, "\x1f"), nil
}

// stripReturnParentheses drops parentheses that wrap an entire RETURN
// expression. Trino's SHOW CREATE FUNCTION adds them around compound
// expressions, so `RETURN x + 1` and the stored `RETURN (x + 1)` compare equal
// while `RETURN (x + 1) * 2` keeps its meaningful grouping.
func stripReturnParentheses(tokens []token) []token {
	for i := 0; i < len(tokens); i++ {
		if !tokens[i].isWord("return") {
			continue
		}
		for {
			end := returnExpressionEnd(tokens, i+1)
			if end <= i+1 || !tokens[i+1].isSymbol("(") {
				break
			}
			close, err := matchingParen(tokens, i+1)
			if err != nil || close != end-1 {
				break
			}
			updated := make([]token, 0, len(tokens)-2)
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
func returnExpressionEnd(tokens []token, start int) int {
	depth := 0
	for i := start; i < len(tokens); i++ {
		switch {
		case tokens[i].isSymbol("("):
			depth++
		case tokens[i].isSymbol(")"):
			if depth == 0 {
				return i
			}
			depth--
		case tokens[i].isSymbol(";") && depth == 0:
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
	input := trimStatement(definition)
	tokens, err := tokenize(input)
	if err != nil {
		return nil, err
	}
	if len(tokens) == 0 || !tokens[0].isSymbol("(") {
		return nil, errors.New("the definition must start with a parameter list, for example (x bigint)")
	}
	close, err := matchingParen(tokens, 0)
	if err != nil {
		return nil, err
	}

	parameterRanges := make([][2]int, 0, 2)
	start := 1
	depth := 0
	for i := 1; i < close; i++ {
		switch {
		case tokens[i].isSymbol("("):
			depth++
		case tokens[i].isSymbol(")"):
			depth--
		case tokens[i].isSymbol(",") && depth == 0:
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
		if name.kind != tokenWord && name.kind != tokenQuotedIdentifier {
			return nil, fmt.Errorf("parameter %q has no name", name.text)
		}
		if bounds[1]-bounds[0] < 2 {
			return nil, fmt.Errorf("parameter %q has no data type", name.text)
		}
		typeStart := tokens[bounds[0]+1].start
		typeEnd := tokens[bounds[1]-1].end
		parameterType := strings.TrimSpace(input[typeStart:typeEnd])
		if parameterType == "" {
			return nil, fmt.Errorf("parameter %q has no data type", name.text)
		}
		parameterTypes = append(parameterTypes, parameterType)
	}

	returns := false
	for i := close + 1; i < len(tokens); i++ {
		if tokens[i].isWord("returns") {
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
	tokens, err := tokenize(parameterType)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(parameterType))
	}
	parts := make([]string, 0, len(tokens))
	for _, t := range tokens {
		switch t.kind {
		case tokenWord:
			parts = append(parts, strings.ToLower(t.text))
		case tokenQuotedIdentifier:
			parts = append(parts, t.text)
		case tokenString:
			parts = append(parts, "'"+t.text+"'")
		default:
			parts = append(parts, t.text)
		}
	}
	return strings.Join(parts, "\x1f")
}

func splitTopLevel(input string) []string {
	tokens, err := tokenize(input)
	if err != nil {
		return []string{strings.TrimSpace(input)}
	}
	return splitRanges(input, tokens)
}

func splitRanges(input string, tokens []token) []string {
	if len(tokens) == 0 {
		return nil
	}
	var parts []string
	depth := 0
	start := tokens[0].start
	for i, t := range tokens {
		switch {
		case t.isSymbol("("):
			depth++
		case t.isSymbol(")"):
			depth--
		case t.isSymbol(",") && depth == 0:
			parts = append(parts, strings.TrimSpace(input[start:t.start]))
			if i+1 < len(tokens) {
				start = tokens[i+1].start
			}
		}
	}
	parts = append(parts, strings.TrimSpace(input[start:]))
	return parts
}

// trimStatement removes surrounding whitespace and a trailing semicolon so a
// definition can be embedded in a single DDL statement.
func trimStatement(definition string) string {
	trimmed := strings.TrimSpace(definition)
	for strings.HasSuffix(trimmed, ";") {
		trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed, ";"))
	}
	return trimmed
}

// extractDefinition removes the leading CREATE FUNCTION <qualified name> from
// the single-column text SHOW CREATE FUNCTION returns.
func extractDefinition(showCreate string) (string, error) {
	tokens, err := tokenize(showCreate)
	if err != nil {
		return "", err
	}
	if len(tokens) < 3 || !tokens[0].isWord("create") || !tokens[1].isWord("function") {
		return "", errors.New("SHOW CREATE FUNCTION did not return a CREATE FUNCTION statement")
	}
	position := 2
	if tokens[position].kind != tokenWord && tokens[position].kind != tokenQuotedIdentifier {
		return "", errors.New("SHOW CREATE FUNCTION returned an unexpected function name")
	}
	lastNameToken := position
	position++
	for position+1 < len(tokens) && tokens[position].isSymbol(".") {
		if tokens[position+1].kind != tokenWord && tokens[position+1].kind != tokenQuotedIdentifier {
			return "", errors.New("SHOW CREATE FUNCTION returned an unexpected qualified name")
		}
		lastNameToken = position + 1
		position += 2
	}

	definition := strings.TrimSpace(showCreate[tokens[lastNameToken].end:])
	if !strings.HasPrefix(definition, "(") {
		return "", errors.New("SHOW CREATE FUNCTION returned a definition without a parameter list")
	}
	return definition, nil
}

// validateDefinition checks the parts the provider itself needs: a parameter
// list for DROP FUNCTION and a RETURNS clause for CREATE FUNCTION. Trino
// remains the authority on the rest of the routine.
func validateDefinition(definition string) error {
	if trimStatement(definition) == "" {
		return errors.New("definition must not be empty")
	}
	_, err := parseSignature(definition)
	return err
}
