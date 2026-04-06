package tui

// Version is injected at build time via -ldflags "-X main.version=..."
// which sets it through main.go's init. Defaults to "dev" when built without ldflags.
var Version = "dev"
