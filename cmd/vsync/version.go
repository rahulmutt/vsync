package main

// Build-time metadata injected via -ldflags "-X main.version=... -X main.commit=... -X main.date=...".
// Defaults are used for local/dev builds.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)
