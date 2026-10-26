package resourceutil

import (
	"errors"
	"fmt"
	"strings"
)

// SQLTokenKind identifies the lexical class of an SQL token.
type SQLTokenKind int

const (
	// SQLWord is an unquoted word: a keyword or an unquoted identifier.
	SQLWord SQLTokenKind = iota
	// SQLQuotedIdentifier is a double-quoted identifier, with the quotes
	// removed and doubled quotes unescaped.
	SQLQuotedIdentifier
	// SQLString is a single-quoted string literal, with the quotes removed and
	// doubled quotes unescaped.
	SQLString
	// SQLSymbol is one punctuation or operator byte.
	SQLSymbol
)

// SQLToken is one lexical unit of an SQL statement. Start and End are byte
// offsets into the input, so callers can recover the original text of a range.
type SQLToken struct {
	Kind  SQLTokenKind
	Text  string
	Start int
	End   int
}

// IsSymbol reports whether the token is the given single-byte symbol.
func (t SQLToken) IsSymbol(symbol string) bool {
	return t.Kind == SQLSymbol && t.Text == symbol
}

// IsWord reports whether the token is an unquoted word matching word
// case-insensitively.
func (t SQLToken) IsWord(word string) bool {
	return t.Kind == SQLWord && strings.EqualFold(t.Text, word)
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

// TokenizeSQL splits SQL into words, quoted identifiers, string literals and
// single-byte symbols, skipping whitespace and SQL comments. It records byte
// offsets so callers can recover the original text of a range.
func TokenizeSQL(input string) ([]SQLToken, error) {
	tokens := make([]SQLToken, 0, 32)
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
			tokens = append(tokens, SQLToken{Kind: SQLString, Text: value, Start: i, End: next})
			i = next
		case c == '"':
			value, next, err := readQuoted(input, i, '"')
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, SQLToken{Kind: SQLQuotedIdentifier, Text: value, Start: i, End: next})
			i = next
		case isSymbolByte(c):
			tokens = append(tokens, SQLToken{Kind: SQLSymbol, Text: input[i : i+1], Start: i, End: i + 1})
			i++
		default:
			start := i
			for i < len(input) && !isSpaceByte(input[i]) && !isSymbolByte(input[i]) &&
				input[i] != '\'' && input[i] != '"' {
				i++
			}
			tokens = append(tokens, SQLToken{Kind: SQLWord, Text: input[start:i], Start: start, End: i})
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

// MatchingParen returns the index of the closing parenthesis that pairs with
// the opening parenthesis at open.
func MatchingParen(tokens []SQLToken, open int) (int, error) {
	if open >= len(tokens) || !tokens[open].IsSymbol("(") {
		return 0, errors.New("expected an opening parenthesis")
	}
	depth := 0
	for i := open; i < len(tokens); i++ {
		switch {
		case tokens[i].IsSymbol("("):
			depth++
		case tokens[i].IsSymbol(")"):
			depth--
			if depth == 0 {
				return i, nil
			}
		}
	}
	return 0, errors.New("unbalanced parentheses")
}

// TrimStatement removes surrounding whitespace and trailing semicolons so a
// fragment can be embedded in a single DDL statement.
func TrimStatement(statement string) string {
	trimmed := strings.TrimSpace(statement)
	for strings.HasSuffix(trimmed, ";") {
		trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed, ";"))
	}
	return trimmed
}

// EquivalentSQL reports whether two SQL fragments are the same after
// normalising unquoted-word case, whitespace and comments. String literals and
// quoted identifiers are compared exactly. Symbol sequences, including
// parentheses, must match, so this is suitable for property expressions whose
// formatting the server may change but whose grouping it preserves.
func EquivalentSQL(left, right string) bool {
	a, err := canonicalSQL(left)
	if err != nil {
		return false
	}
	b, err := canonicalSQL(right)
	if err != nil {
		return false
	}
	return a == b
}

func canonicalSQL(input string) (string, error) {
	tokens, err := TokenizeSQL(TrimStatement(input))
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, len(tokens))
	for _, token := range tokens {
		switch token.Kind {
		case SQLWord:
			parts = append(parts, "w:"+strings.ToLower(token.Text))
		case SQLQuotedIdentifier:
			parts = append(parts, "i:"+token.Text)
		case SQLString:
			parts = append(parts, "s:"+token.Text)
		default:
			parts = append(parts, "p:"+token.Text)
		}
	}
	return strings.Join(parts, "\x1f"), nil
}
