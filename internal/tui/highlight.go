package tui

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// highlighter applies syntax highlighting to code blocks in agent output.
type highlighter struct {
	language string
	style    *chroma.Style
}

// newHighlighter creates a highlighter for the given programming language.
func newHighlighter(language string) *highlighter {
	return &highlighter{
		language: normalizeLanguage(language),
		style:    styles.Get("dracula"),
	}
}

// highlightLines applies syntax coloring to lines that appear to be source code.
// Code blocks delimited by ``` fences are highlighted; other lines pass through.
func (h *highlighter) highlightLines(lines []string) []string {
	if h == nil || h.language == "" {
		return lines
	}

	result := make([]string, 0, len(lines))
	var codeBlock []string
	inFence := false
	fenceLang := ""

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if !inFence && isFenceOpen(trimmed) {
			inFence = true
			fenceLang = extractFenceLang(trimmed)
			codeBlock = nil
			result = append(result, line) // keep fence marker as-is
			continue
		}

		if inFence && isFenceClose(trimmed) {
			// Highlight accumulated code block.
			lang := fenceLang
			if lang == "" {
				lang = h.language
			}
			highlighted := h.highlightBlock(codeBlock, lang)
			result = append(result, highlighted...)
			result = append(result, line) // keep closing fence
			inFence = false
			codeBlock = nil
			fenceLang = ""
			continue
		}

		if inFence {
			codeBlock = append(codeBlock, line)
		} else {
			result = append(result, line)
		}
	}

	// If we ended mid-fence, highlight what we have so far.
	if inFence && len(codeBlock) > 0 {
		lang := fenceLang
		if lang == "" {
			lang = h.language
		}
		highlighted := h.highlightBlock(codeBlock, lang)
		result = append(result, highlighted...)
	}

	return result
}

// highlightBlock highlights a slice of code lines.
func (h *highlighter) highlightBlock(lines []string, lang string) []string {
	if len(lines) == 0 {
		return lines
	}

	code := strings.Join(lines, "\n")
	highlighted := highlightCode(code, lang, h.style)
	return strings.Split(highlighted, "\n")
}

// highlightCode applies chroma syntax highlighting and returns ANSI-colored text.
func highlightCode(code, language string, style *chroma.Style) string {
	lexer := lexers.Get(language)
	if lexer == nil {
		lexer = lexers.Analyse(code)
	}
	if lexer == nil {
		return code
	}
	lexer = chroma.Coalesce(lexer)

	formatter := formatters.Get("terminal256")
	if formatter == nil {
		return code
	}

	if style == nil {
		style = styles.Get("dracula")
	}

	iterator, err := lexer.Tokenise(nil, code)
	if err != nil {
		return code
	}

	var sb strings.Builder
	if err := formatter.Format(&sb, style, iterator); err != nil {
		return code
	}

	return sb.String()
}

// isFenceOpen checks if a line opens a code fence.
func isFenceOpen(trimmed string) bool {
	return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
}

// isFenceClose checks if a line closes a code fence.
func isFenceClose(trimmed string) bool {
	return trimmed == "```" || trimmed == "~~~"
}

// extractFenceLang extracts the language hint from a fence opening line.
func extractFenceLang(trimmed string) string {
	tag := ""
	if strings.HasPrefix(trimmed, "```") {
		tag = strings.TrimPrefix(trimmed, "```")
	} else if strings.HasPrefix(trimmed, "~~~") {
		tag = strings.TrimPrefix(trimmed, "~~~")
	}
	tag = strings.TrimSpace(tag)

	// Strip any file path or attributes after the language.
	if idx := strings.IndexAny(tag, " \t:"); idx > 0 {
		tag = tag[:idx]
	}

	return normalizeLanguage(tag)
}

// normalizeLanguage maps common language names to chroma lexer names.
func normalizeLanguage(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "go", "golang":
		return "Go"
	case "rust", "rs":
		return "Rust"
	case "python", "py":
		return "Python"
	case "javascript", "js":
		return "JavaScript"
	case "typescript", "ts":
		return "TypeScript"
	case "tsx":
		return "TypeScript"
	case "jsx":
		return "JavaScript"
	case "java":
		return "Java"
	case "ruby", "rb":
		return "Ruby"
	case "c":
		return "C"
	case "cpp", "c++", "cc":
		return "C++"
	case "csharp", "cs", "c#":
		return "C#"
	case "swift":
		return "Swift"
	case "kotlin", "kt":
		return "Kotlin"
	case "php":
		return "PHP"
	case "html":
		return "HTML"
	case "css":
		return "CSS"
	case "scss":
		return "SCSS"
	case "sql":
		return "SQL"
	case "shell", "bash", "sh", "zsh":
		return "Bash"
	case "yaml", "yml":
		return "YAML"
	case "json":
		return "JSON"
	case "toml":
		return "TOML"
	case "xml":
		return "XML"
	case "markdown", "md":
		return "Markdown"
	case "dockerfile", "docker":
		return "Docker"
	case "terraform", "tf", "hcl":
		return "Terraform"
	case "protobuf", "proto":
		return "Protocol Buffer"
	case "graphql", "gql":
		return "GraphQL"
	case "lua":
		return "Lua"
	case "dart":
		return "Dart"
	case "scala":
		return "Scala"
	case "":
		return ""
	default:
		return lang
	}
}
