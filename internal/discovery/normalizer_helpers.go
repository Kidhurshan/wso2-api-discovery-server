package discovery

import "regexp"

// NewNormalizerFromRegexes is a convenience constructor for callers that
// already have compiled regex slices (e.g. tests in other packages, or
// out-of-band setup that doesn't go through config.Validate).
//
// Production code should use NewFromConfig — it pulls from the validated
// config object so the patterns match what the operator declared.
func NewNormalizerFromRegexes(builtin, user, exclude []*regexp.Regexp) *Normalizer {
	return &Normalizer{
		Version: "v2",
		builtin: builtin,
		user:    user,
		exclude: exclude,
	}
}
