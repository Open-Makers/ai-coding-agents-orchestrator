package runner

import (
	"fmt"
	"strings"
)

func parseSections(output string, keys []string) (map[string]string, error) {
	sections := make(map[string]string, len(keys))
	current := ""
	lines := strings.Split(output, "\n")
	keySet := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		keySet[k] = struct{}{}
	}

	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "===") && strings.HasSuffix(trim, "===") {
			name := strings.Trim(trim, "=")
			name = strings.TrimSpace(name)
			if _, ok := keySet[name]; ok {
				current = name
				if _, exists := sections[name]; !exists {
					sections[name] = ""
				}
				continue
			}
		}
		if current != "" {
			sections[current] += line + "\n"
		}
	}

	for _, k := range keys {
		if _, ok := sections[k]; !ok {
			return nil, fmt.Errorf("missing section %q", k)
		}
		sections[k] = strings.TrimSpace(sections[k])
	}
	return sections, nil
}
