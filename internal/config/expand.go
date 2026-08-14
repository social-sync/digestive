package config

import (
	"fmt"
	"regexp"
	"strings"
)

// varRef matches ${NAME} and ${NAME:-default}.
var varRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-[^}]*)?\}`)

// expand substitutes ${VAR} / ${VAR:-default} references in s using lookup.
// A reference with no value and no default is a hard error, so a missing
// secret fails loudly rather than silently exporting with an empty value.
func expand(s string, lookup func(string) (string, bool)) (string, error) {
	var missing []string

	out := varRef.ReplaceAllStringFunc(s, func(match string) string {
		groups := varRef.FindStringSubmatch(match)
		name := groups[1]
		hasDefault := groups[2] != ""
		def := strings.TrimPrefix(groups[2], ":-")

		if v, ok := lookup(name); ok {
			return v
		}
		if hasDefault {
			return def
		}
		missing = append(missing, name)
		return match
	})

	if len(missing) > 0 {
		return "", fmt.Errorf("unset environment variable(s) referenced in config: %s", strings.Join(missing, ", "))
	}
	return out, nil
}
