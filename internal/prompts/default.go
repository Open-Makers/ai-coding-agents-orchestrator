package prompts

// defaultLoader is the package-level loader instance.
var defaultLoader = New()

// Load returns the content of a prompt template by name.
func Load(name string) (string, error) {
	return defaultLoader.Load(name)
}

// MustLoad returns the content of a prompt or panics if not found.
func MustLoad(name string) string {
	return defaultLoader.MustLoad(name)
}
