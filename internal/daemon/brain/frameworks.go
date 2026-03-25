package brain

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FrameworkResult holds detection results for frameworks, patterns, testing, and CI/CD.
type FrameworkResult struct {
	Frameworks []string `json:"frameworks"`
	Patterns   []string `json:"patterns"`
	Testing    []string `json:"testing"`
	CICD       []string `json:"ci_cd"`
}

// DetectFrameworks heuristically detects frameworks, patterns, testing, and CI/CD.
func DetectFrameworks(root string, files []FileInfo, externalDeps []string) FrameworkResult {
	depSet := make(map[string]bool)
	for _, d := range externalDeps {
		depSet[strings.ToLower(d)] = true
	}
	fileNames := make(map[string]bool)
	filePaths := make(map[string]bool)
	for _, f := range files {
		parts := strings.Split(f.Path, "/")
		fileNames[parts[len(parts)-1]] = true
		filePaths[f.Path] = true
	}

	var frameworks, patterns, testing, cicd []string

	// Python frameworks
	check := func(dep, name string) {
		if depSet[dep] {
			frameworks = append(frameworks, name)
		}
	}
	check("django", "Django")
	check("flask", "Flask")
	check("fastapi", "FastAPI")
	check("starlette", "Starlette")
	check("celery", "Celery")
	check("anthropic", "Anthropic SDK")
	check("openai", "OpenAI SDK")
	check("pydantic", "Pydantic")
	check("sqlalchemy", "SQLAlchemy")
	check("alembic", "Alembic")

	// JS/TS frameworks
	check("react", "React")
	if depSet["next"] || depSet["next.js"] {
		frameworks = append(frameworks, "Next.js")
	}
	check("vue", "Vue.js")
	check("nuxt", "Nuxt.js")
	check("express", "Express")
	if depSet["nestjs"] || depSet["@nestjs/core"] {
		frameworks = append(frameworks, "NestJS")
	}
	check("svelte", "Svelte")

	// Rust
	check("actix-web", "Actix Web")
	check("tokio", "Tokio")
	check("axum", "Axum")

	// Go
	if depSet["gin-gonic/gin"] {
		frameworks = append(frameworks, "Gin")
	}

	// Testing
	if depSet["pytest"] {
		testing = append(testing, "pytest")
	}
	if depSet["unittest"] {
		testing = append(testing, "unittest")
	}
	if depSet["jest"] {
		testing = append(testing, "Jest")
	}
	if depSet["mocha"] {
		testing = append(testing, "Mocha")
	}
	if depSet["vitest"] {
		testing = append(testing, "Vitest")
	}
	// Check for conftest.py
	hasConftest := false
	for p := range filePaths {
		if strings.Contains(p, "conftest.py") {
			hasConftest = true
			break
		}
	}
	if hasConftest {
		found := false
		for _, t := range testing {
			if t == "pytest" {
				found = true
				break
			}
		}
		if !found {
			testing = append(testing, "pytest")
		}
	}

	// CI/CD
	if info, err := os.Stat(filepath.Join(root, ".github", "workflows")); err == nil && info.IsDir() {
		cicd = append(cicd, "GitHub Actions")
	}
	if _, err := os.Stat(filepath.Join(root, ".gitlab-ci.yml")); err == nil {
		cicd = append(cicd, "GitLab CI")
	}
	if _, err := os.Stat(filepath.Join(root, "Jenkinsfile")); err == nil {
		cicd = append(cicd, "Jenkins")
	}
	if info, err := os.Stat(filepath.Join(root, ".circleci")); err == nil && info.IsDir() {
		cicd = append(cicd, "CircleCI")
	}
	if _, err := os.Stat(filepath.Join(root, ".travis.yml")); err == nil {
		cicd = append(cicd, "Travis CI")
	}

	// Patterns
	if fileNames["Dockerfile"] || fileNames["docker-compose.yml"] {
		patterns = append(patterns, "Docker")
	}
	for _, f := range files {
		if strings.Contains(strings.ToLower(f.Path), "middleware") {
			patterns = append(patterns, "Middleware")
			break
		}
	}
	for _, f := range files {
		if strings.Contains(strings.ToLower(f.Path), "migration") {
			patterns = append(patterns, "Database Migrations")
			break
		}
	}
	for _, f := range files {
		if strings.HasSuffix(f.Path, ".proto") {
			patterns = append(patterns, "Protocol Buffers")
			break
		}
	}
	for _, f := range files {
		if strings.HasSuffix(f.Path, ".graphql") || strings.HasSuffix(f.Path, ".gql") {
			patterns = append(patterns, "GraphQL")
			break
		}
	}

	sort.Strings(frameworks)
	sort.Strings(patterns)
	sort.Strings(testing)
	sort.Strings(cicd)

	return FrameworkResult{
		Frameworks: dedupStrings(frameworks),
		Patterns:   dedupStrings(patterns),
		Testing:    dedupStrings(testing),
		CICD:       dedupStrings(cicd),
	}
}

func dedupStrings(s []string) []string {
	if len(s) == 0 {
		return s
	}
	seen := make(map[string]bool)
	var result []string
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			result = append(result, v)
		}
	}
	return result
}
