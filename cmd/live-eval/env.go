package main

import (
	"fmt"
	"os"
	"strings"
)

// loadLLMEnvFile loads provider variables from a dotenv-style file.
//
// Only LLM_* variables are considered. The shared local secrets file also
// contains DATABASE_URL/JWT_SECRET, and live-eval must never need (or load)
// them. An already-set environment variable wins, so a single value can be
// overridden for one run without editing the file. The values are never logged.
func loadLLMEnvFile(path string) (loaded, ignored int, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, fmt.Errorf("read env file: %w", err)
	}
	for lineNumber, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return 0, 0, fmt.Errorf("env file %s line %d: want KEY=VALUE", path, lineNumber+1)
		}
		if !validEnvName(key) {
			return 0, 0, fmt.Errorf("env file %s line %d: invalid variable name %q", path, lineNumber+1, key)
		}
		if !strings.HasPrefix(key, "LLM_") {
			ignored++
			continue
		}
		if _, alreadySet := os.LookupEnv(key); alreadySet {
			ignored++
			continue
		}
		if err := os.Setenv(key, dotenvValue(value)); err != nil {
			return 0, 0, fmt.Errorf("env file %s line %d: set %s: %w", path, lineNumber+1, key, err)
		}
		loaded++
	}
	return loaded, ignored, nil
}

func validEnvName(name string) bool {
	for i, r := range name {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return name != ""
}

// dotenvValue strips the optional matching quotes around a value. It never
// expands variables or interprets escapes: provider keys and URLs must be taken
// literally.
func dotenvValue(raw string) string {
	value := strings.TrimSpace(raw)
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') ||
			(value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}
