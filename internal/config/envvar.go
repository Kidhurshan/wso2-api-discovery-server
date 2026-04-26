package config

import (
	"os"
	"regexp"
)

// envVarPattern matches ${NAME} and ${NAME:-default}.
//
// Capture groups:
//
//	1: variable name
//	2: ":-" sentinel (present when a default is supplied)
//	3: default value (may be empty when group 2 is present)
var envVarPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-)?([^}]*)\}`)

// Expand replaces ${VAR} and ${VAR:-default} occurrences in s with values from
// the process environment. Unknown variables without a default expand to "".
//
// Per operations_guide.md §1.3 the loader applies this to the raw TOML text
// once, before parsing. Expanded values are never logged.
func Expand(s string) string {
	return envVarPattern.ReplaceAllStringFunc(s, func(match string) string {
		groups := envVarPattern.FindStringSubmatch(match)
		name := groups[1]
		hasDefault := groups[2] == ":-"
		defaultVal := groups[3]

		if v, ok := os.LookupEnv(name); ok {
			return v
		}
		if hasDefault {
			return defaultVal
		}
		return ""
	})
}
