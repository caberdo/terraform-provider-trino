package materializedview

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/caberdo/terraform-provider-trino/internal/resourceutil"
	"github.com/caberdo/terraform-provider-trino/internal/trino"
)

// createStatement builds CREATE MATERIALIZED VIEW for the configured model.
// properties holds raw SQL expressions, so their keys - the only identifiers
// the provider authors - are quoted while the values are emitted verbatim.
func createStatement(value model, properties map[string]string, gracePeriod string) string {
	var b strings.Builder
	b.WriteString("CREATE MATERIALIZED VIEW ")
	b.WriteString(qualifiedName(value))
	if gracePeriod != "" {
		b.WriteString("\nGRACE PERIOD ")
		b.WriteString(gracePeriod)
	}
	if len(properties) > 0 {
		b.WriteString("\nWITH (")
		keys := make([]string, 0, len(properties))
		for key := range properties {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for i, key := range keys {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString("\n  ")
			b.WriteString(trino.QuoteIdentifier(key))
			b.WriteString(" = ")
			b.WriteString(strings.TrimSpace(properties[key]))
		}
		b.WriteString("\n)")
	}
	b.WriteString("\nAS\n")
	b.WriteString(resourceutil.TrimStatement(value.Query.ValueString()))
	return b.String()
}

func refreshStatement(value model) string {
	return "REFRESH MATERIALIZED VIEW " + qualifiedName(value)
}

func dropStatement(value model) string {
	return "DROP MATERIALIZED VIEW IF EXISTS " + qualifiedName(value)
}

func qualifiedName(value model) string {
	return trino.QuoteQualified(
		value.Catalog.ValueString(),
		value.Schema.ValueString(),
		value.Name.ValueString(),
	)
}

// renderGracePeriod validates a Terraform duration and renders the GRACE
// PERIOD interval literal. Trino accepts day-to-time interval literals, and
// whole seconds are the finest granularity the provider renders; a sub-second
// duration is rejected instead of silently rounded.
func renderGracePeriod(duration string) (string, error) {
	trimmed := strings.TrimSpace(duration)
	if trimmed == "" {
		return "", errors.New("grace_period must not be empty")
	}
	parsed, err := time.ParseDuration(trimmed)
	if err != nil {
		return "", fmt.Errorf("%q is not a duration, for example 1h or 30m", duration)
	}
	if parsed <= 0 {
		return "", errors.New("grace_period must be positive")
	}
	if parsed%time.Second != 0 {
		return "", errors.New("grace_period must be a whole number of seconds")
	}
	seconds := strconv.FormatInt(int64(parsed/time.Second), 10)
	return "INTERVAL '" + seconds + "' SECOND", nil
}

// showCreate is the definition read back from SHOW CREATE MATERIALIZED VIEW.
// properties holds the property expressions as written by Trino; the provider
// compares only the keys configured in state, because Iceberg also reports
// values derived from the storage table.
type showCreateStatement struct {
	query      string
	properties map[string]string
}

// parseShowCreate extracts the query and the storage properties from the
// single-column text SHOW CREATE MATERIALIZED VIEW returns. The statement
// carries optional GRACE PERIOD, WHEN STALE, COMMENT and WITH clauses between
// the name and AS; the query is the remaining text.
func parseShowCreate(showCreate string) (showCreateStatement, error) {
	var result showCreateStatement

	tokens, err := resourceutil.TokenizeSQL(showCreate)
	if err != nil {
		return result, err
	}
	if len(tokens) < 4 || !tokens[0].IsWord("create") || !tokens[1].IsWord("materialized") || !tokens[2].IsWord("view") {
		return result, errors.New("SHOW CREATE MATERIALIZED VIEW did not return a CREATE MATERIALIZED VIEW statement")
	}
	if !isNameToken(tokens[3]) {
		return result, errors.New("SHOW CREATE MATERIALIZED VIEW returned an unexpected view name")
	}
	position := 4
	for position+1 < len(tokens) && tokens[position].IsSymbol(".") {
		if !isNameToken(tokens[position+1]) {
			return result, errors.New("SHOW CREATE MATERIALIZED VIEW returned an unexpected qualified view name")
		}
		position += 2
	}

	for position < len(tokens) {
		switch {
		case tokens[position].IsWord("as"):
			if position+1 >= len(tokens) {
				return result, errors.New("SHOW CREATE MATERIALIZED VIEW returned no query after AS")
			}
			result.query = resourceutil.TrimStatement(showCreate[tokens[position+1].Start:])
			if result.query == "" {
				return result, errors.New("SHOW CREATE MATERIALIZED VIEW returned an empty query")
			}
			return result, nil
		case tokens[position].IsWord("grace") && hasWord(tokens, position+1, "period"):
			// The interval literal is not read back; skip it up to the next
			// clause keyword.
			position += 2
			for position < len(tokens) && !isClauseStart(tokens[position]) {
				position++
			}
		case tokens[position].IsWord("when") && hasWord(tokens, position+1, "stale"):
			position += 3
		case tokens[position].IsWord("comment"):
			if position+1 >= len(tokens) || tokens[position+1].Kind != resourceutil.SQLString {
				return result, errors.New("SHOW CREATE MATERIALIZED VIEW returned an unexpected COMMENT clause")
			}
			position += 2
		case tokens[position].IsWord("with"):
			if position+1 >= len(tokens) || !tokens[position+1].IsSymbol("(") {
				return result, errors.New("SHOW CREATE MATERIALIZED VIEW returned an unexpected WITH clause")
			}
			close, err := resourceutil.MatchingParen(tokens, position+1)
			if err != nil {
				return result, err
			}
			properties, err := parseProperties(showCreate, tokens, position+1, close)
			if err != nil {
				return result, err
			}
			result.properties = properties
			position = close + 1
		default:
			return result, fmt.Errorf("SHOW CREATE MATERIALIZED VIEW returned an unexpected token %q", tokens[position].Text)
		}
	}
	return result, errors.New("SHOW CREATE MATERIALIZED VIEW returned no AS clause")
}

// parseProperties splits the WITH (...) list. Values keep the text Trino
// formatted, so a caller can compare them with the configured expression.
func parseProperties(input string, tokens []resourceutil.SQLToken, open, close int) (map[string]string, error) {
	properties := make(map[string]string)
	starts := []int{open + 1}
	depth := 0
	for i := open + 1; i < close; i++ {
		switch {
		case tokens[i].IsSymbol("("), tokens[i].IsSymbol("["), tokens[i].IsSymbol("{"):
			depth++
		case tokens[i].IsSymbol(")"), tokens[i].IsSymbol("]"), tokens[i].IsSymbol("}"):
			depth--
		case tokens[i].IsSymbol(",") && depth == 0:
			starts = append(starts, i+1)
		}
	}

	bounds := make([][2]int, 0, len(starts))
	for i, start := range starts {
		end := close
		if i+1 < len(starts) {
			end = starts[i+1] - 1
		}
		if start >= end {
			continue
		}
		bounds = append(bounds, [2]int{start, end})
	}

	for _, bound := range bounds {
		name := tokens[bound[0]]
		if !isNameToken(name) {
			return nil, fmt.Errorf("SHOW CREATE MATERIALIZED VIEW returned an unexpected property name %q", name.Text)
		}
		if bound[0]+2 >= bound[1] || !tokens[bound[0]+1].IsSymbol("=") {
			return nil, fmt.Errorf("SHOW CREATE MATERIALIZED VIEW returned a malformed value for property %q", name.Text)
		}
		value := strings.TrimSpace(input[tokens[bound[0]+2].Start:tokens[bound[1]-1].End])
		if value == "" {
			return nil, fmt.Errorf("SHOW CREATE MATERIALIZED VIEW returned an empty value for property %q", name.Text)
		}
		properties[strings.ToLower(name.Text)] = value
	}
	return properties, nil
}

func isNameToken(token resourceutil.SQLToken) bool {
	return token.Kind == resourceutil.SQLWord || token.Kind == resourceutil.SQLQuotedIdentifier
}

func hasWord(tokens []resourceutil.SQLToken, position int, word string) bool {
	return position < len(tokens) && tokens[position].IsWord(word)
}

// isClauseStart reports whether the token begins a clause that can follow GRACE
// PERIOD in a SHOW CREATE MATERIALIZED VIEW statement.
func isClauseStart(token resourceutil.SQLToken) bool {
	return token.IsWord("when") || token.IsWord("comment") || token.IsWord("with") || token.IsWord("as")
}
