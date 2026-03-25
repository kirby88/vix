package brain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ParseDependencies extracts external dependency names from common config files.
func ParseDependencies(root string) []string {
	deps := make(map[string]bool)

	// requirements*.txt
	matches, _ := filepath.Glob(filepath.Join(root, "requirements*.txt"))
	for _, m := range matches {
		for _, d := range parseRequirementsTxt(m) {
			deps[d] = true
		}
	}

	// pyproject.toml
	if data, err := os.ReadFile(filepath.Join(root, "pyproject.toml")); err == nil {
		for _, d := range parsePyprojectToml(string(data)) {
			deps[d] = true
		}
	}

	// package.json
	if data, err := os.ReadFile(filepath.Join(root, "package.json")); err == nil {
		for _, d := range parsePackageJSON(data) {
			deps[d] = true
		}
	}

	// Cargo.toml
	if data, err := os.ReadFile(filepath.Join(root, "Cargo.toml")); err == nil {
		for _, d := range parseCargoToml(string(data)) {
			deps[d] = true
		}
	}

	// go.mod
	if data, err := os.ReadFile(filepath.Join(root, "go.mod")); err == nil {
		for _, d := range parseGoMod(string(data)) {
			deps[d] = true
		}
	}

	// Gemfile
	if data, err := os.ReadFile(filepath.Join(root, "Gemfile")); err == nil {
		for _, d := range parseGemfile(string(data)) {
			deps[d] = true
		}
	}

	var result []string
	for d := range deps {
		result = append(result, d)
	}
	sort.Strings(result)
	LogInfo("Found %d external dependencies", len(result))
	return result
}

func parseRequirementsTxt(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var deps []string
	re := regexp.MustCompile(`^([a-zA-Z0-9_-]+)`)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
			continue
		}
		// Strip version specifiers
		parts := regexp.MustCompile(`[>=<!\[;]`).Split(line, 2)
		name := strings.TrimSpace(parts[0])
		if name != "" && re.MatchString(name) {
			deps = append(deps, name)
		}
	}
	return deps
}

func parsePyprojectToml(text string) []string {
	var deps []string
	inDeps := false
	re := regexp.MustCompile(`^["']([a-zA-Z0-9_-]+)`)
	for _, line := range strings.Split(text, "\n") {
		stripped := strings.TrimSpace(line)
		if stripped == "dependencies = [" {
			inDeps = true
			continue
		}
		if inDeps {
			if stripped == "]" {
				inDeps = false
				continue
			}
			if m := re.FindStringSubmatch(stripped); m != nil {
				deps = append(deps, m[1])
			}
		}
	}
	return deps
}

func parsePackageJSON(data []byte) []string {
	var pkg map[string]any
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil
	}
	var deps []string
	for _, key := range []string{"dependencies", "devDependencies"} {
		section, ok := pkg[key].(map[string]any)
		if !ok {
			continue
		}
		for name := range section {
			deps = append(deps, name)
		}
	}
	return deps
}

func parseCargoToml(text string) []string {
	var deps []string
	inDeps := false
	re := regexp.MustCompile(`^([a-zA-Z0-9_-]+)\s*=`)
	for _, line := range strings.Split(text, "\n") {
		stripped := strings.TrimSpace(line)
		if stripped == "[dependencies]" || stripped == "[dev-dependencies]" {
			inDeps = true
			continue
		}
		if strings.HasPrefix(stripped, "[") && inDeps {
			inDeps = false
			continue
		}
		if inDeps {
			if m := re.FindStringSubmatch(stripped); m != nil {
				deps = append(deps, m[1])
			}
		}
	}
	return deps
}

func parseGoMod(text string) []string {
	var deps []string
	inRequire := false
	for _, line := range strings.Split(text, "\n") {
		stripped := strings.TrimSpace(line)
		if strings.HasPrefix(stripped, "require (") {
			inRequire = true
			continue
		}
		if stripped == ")" && inRequire {
			inRequire = false
			continue
		}
		if inRequire {
			parts := strings.Fields(stripped)
			if len(parts) > 0 {
				deps = append(deps, parts[0])
			}
		} else if strings.HasPrefix(stripped, "require ") {
			parts := strings.Fields(stripped)
			if len(parts) >= 2 {
				deps = append(deps, parts[1])
			}
		}
	}
	return deps
}

func parseGemfile(text string) []string {
	var deps []string
	re := regexp.MustCompile(`gem\s+['"]([^'"]+)['"]`)
	for _, line := range strings.Split(text, "\n") {
		if m := re.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			deps = append(deps, m[1])
		}
	}
	return deps
}
