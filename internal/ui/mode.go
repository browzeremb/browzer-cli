package ui

// LLMMode, when true, suppresses the brand banner, disables ANSI colors,
// and tells the spinner to skip animation. Set from PersistentPreRunE in
// internal/commands/root.go when `--llm` or BROWZER_LLM is present.
// banner.go's colorEnabled() checks this first so every downstream caller
// (Heading, spinner, status, table) degrades to plain text automatically.
var LLMMode bool
