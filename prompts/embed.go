// Package prompts embeds the versioned agent prompts that are compiled into the
// binary and loaded by internal/prompts.
package prompts

import "embed"

// FS contains the shared prompt rules and every agent prompt version.
//
//go:embed all:_shared
//go:embed interviewer/v1
//go:embed analyst/v1
//go:embed synthesizer/v1
var FS embed.FS
