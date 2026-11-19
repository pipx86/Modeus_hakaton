// Package prompts embeds the versioned agent prompts that are compiled into the
// binary and loaded by internal/prompts.
package prompts

import "embed"

// FS contains the safe generic fallback prompts. Local development can
// override them with a private PROMPTS_DIR (see internal/prompts).
//
//go:embed all:default
var FS embed.FS
