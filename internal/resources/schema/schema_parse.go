package schema

import (
	"errors"
	"fmt"
	"strings"
)

// Owner is the authorization entry read from SHOW CREATE SCHEMA.
type Owner struct {
	Type string
	Name string
}

type tokenKind int

const (
	tokenEOF tokenKind = iota
	tokenWord
	tokenString
	tokenIdentifier
	tokenLParen
	tokenRParen
	tokenComma
	tokenEquals
	tokenDot
)

type token struct {
	kind tokenKind
	text string
}

func (t token) String() string {
	switch t.kind {
	case tokenEOF:
		return "end of statement"
	case tokenString:
		return "string literal"
	case tokenIdentifier:
		return "quoted identifier"
	default:
		return fmt.Sprintf("%q", t.text)
	}
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

func isWordDelimiter(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '(', ')', ',', '=', '\'', '"', '.':
		return true
	default:
		return false
	}
}

// tokenize splits Trino DDL into words, quoted strings/identifiers and single
// punctuation characters. Quoted values are decoded (” -> ' and "" -> ") but
// keep every other byte, including commas, spaces and newlines.
func tokenize(input string) ([]token, error) {
	tokens := make([]token, 0, 16)
	for i := 0; i < len(input); {
		c := input[i]
		switch {
		case isSpace(c):
			i++
		case c == '(':
			tokens = append(tokens, token{kind: tokenLParen, text: "("})
			i++
		case c == ')':
			tokens = append(tokens, token{kind: tokenRParen, text: ")"})
			i++
		case c == ',':
			tokens = append(tokens, token{kind: tokenComma, text: ","})
			i++
		case c == '=':
			tokens = append(tokens, token{kind: tokenEquals, text: "="})
			i++
		case c == '.':
			tokens = append(tokens, token{kind: tokenDot, text: "."})
			i++
		case c == '\'':
			value, next, err := quoted(input, i, '\'')
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, token{kind: tokenString, text: value})
			i = next
		case c == '"':
			value, next, err := quoted(input, i, '"')
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, token{kind: tokenIdentifier, text: value})
			i = next
		default:
			start := i
			for i < len(input) && !isWordDelimiter(input[i]) {
				i++
			}
			tokens = append(tokens, token{kind: tokenWord, text: input[start:i]})
		}
	}
	tokens = append(tokens, token{kind: tokenEOF})
	return tokens, nil
}

// quoted reads a single- or double-quoted value starting at start. The quote
// character is escaped by doubling it, matching Trino string literals and
// identifiers.
func quoted(input string, start int, quote byte) (string, int, error) {
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

type parser struct {
	tokens []token
	pos    int
}

func (p *parser) peek() token {
	if p.pos >= len(p.tokens) {
		return token{kind: tokenEOF}
	}
	return p.tokens[p.pos]
}

func (p *parser) next() token {
	t := p.peek()
	if p.pos < len(p.tokens)-1 {
		p.pos++
	}
	return t
}

func (p *parser) acceptKeyword(keyword string) bool {
	t := p.peek()
	if t.kind == tokenWord && strings.EqualFold(t.text, keyword) {
		p.next()
		return true
	}
	return false
}

func (p *parser) expectSymbol(kind tokenKind, description string) (token, error) {
	t := p.next()
	if t.kind != kind {
		return token{}, fmt.Errorf("expected %s, found %s", description, t)
	}
	return t, nil
}

// parseQualifiedName consumes one or more dot-separated identifiers. The name
// itself is irrelevant: the resource already knows which schema it asked about.
func (p *parser) parseQualifiedName() error {
	first := p.next()
	if first.kind != tokenWord && first.kind != tokenIdentifier {
		return fmt.Errorf("expected schema name, found %s", first)
	}
	for p.peek().kind == tokenDot {
		p.next()
		part := p.next()
		if part.kind != tokenWord && part.kind != tokenIdentifier {
			return fmt.Errorf("expected identifier after %q, found %s", ".", part)
		}
	}
	return nil
}

// parseShowCreateSchema extracts the authorization owner and the WITH
// properties from the single-column output of SHOW CREATE SCHEMA.
func parseShowCreateSchema(ddl string) (*Owner, map[string]string, error) {
	tokens, err := tokenize(ddl)
	if err != nil {
		return nil, nil, err
	}
	p := &parser{tokens: tokens}

	if !p.acceptKeyword("CREATE") {
		return nil, nil, errors.New("expected CREATE at the start of SHOW CREATE SCHEMA output")
	}
	if !p.acceptKeyword("SCHEMA") {
		t := p.peek()
		return nil, nil, fmt.Errorf("expected SCHEMA after CREATE, found %s", t)
	}
	if err := p.parseQualifiedName(); err != nil {
		return nil, nil, err
	}

	var owner *Owner
	if p.acceptKeyword("AUTHORIZATION") {
		typeToken := p.next()
		if typeToken.kind != tokenWord {
			return nil, nil, fmt.Errorf("expected USER or ROLE after AUTHORIZATION, found %s", typeToken)
		}
		ownerType := strings.ToUpper(typeToken.text)
		if ownerType != "USER" && ownerType != "ROLE" {
			return nil, nil, fmt.Errorf("expected USER or ROLE after AUTHORIZATION, found %q", typeToken.text)
		}
		nameToken := p.next()
		switch nameToken.kind {
		case tokenWord, tokenIdentifier, tokenString:
		default:
			return nil, nil, fmt.Errorf("expected authorization principal, found %s", nameToken)
		}
		if nameToken.text == "" {
			return nil, nil, errors.New("authorization principal is empty")
		}
		owner = &Owner{Type: ownerType, Name: nameToken.text}
	}

	var properties map[string]string
	if p.acceptKeyword("WITH") {
		if _, err := p.expectSymbol(tokenLParen, "'(' after WITH"); err != nil {
			return nil, nil, err
		}
		properties = make(map[string]string)
		if p.peek().kind == tokenRParen {
			p.next()
		} else {
			for {
				keyToken := p.next()
				switch keyToken.kind {
				case tokenWord, tokenIdentifier, tokenString:
				default:
					return nil, nil, fmt.Errorf("expected property name, found %s", keyToken)
				}
				if _, err := p.expectSymbol(tokenEquals, "'=' after property name"); err != nil {
					return nil, nil, err
				}
				valueToken := p.next()
				if valueToken.kind != tokenString {
					return nil, nil, fmt.Errorf("expected string literal for property %q, found %s", keyToken.text, valueToken)
				}
				properties[keyToken.text] = valueToken.text

				separator := p.next()
				if separator.kind == tokenRParen {
					break
				}
				if separator.kind != tokenComma {
					return nil, nil, fmt.Errorf("expected ',' or ')' after property %q, found %s", keyToken.text, separator)
				}
				if p.peek().kind == tokenRParen {
					return nil, nil, errors.New("trailing comma in property list")
				}
			}
		}
	}

	if trailing := p.peek(); trailing.kind != tokenEOF {
		return nil, nil, fmt.Errorf("unexpected %s after schema definition", trailing)
	}

	return owner, properties, nil
}
