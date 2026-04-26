package config

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// Load reads the TOML config at path, applies env-var expansion to the raw
// text (per operations_guide.md §1.3), and returns the parsed Config.
//
// The returned Config has not yet been validated — callers should invoke
// Validate() before using it.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}

	expanded := Expand(string(raw))

	var cfg Config
	if _, err := toml.Decode(expanded, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}

	return &cfg, nil
}
