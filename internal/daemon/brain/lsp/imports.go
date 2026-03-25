package lsp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
)

// ImportLocation describes where in a source file an import module specifier sits.
type ImportLocation struct {
	Line      int    // 0-based line number (LSP convention)
	Character int    // 0-based character offset pointing inside the module string
	Module    string // raw import string (e.g. "github.com/foo/bar", "./utils", "numpy")
}

// ResolveImport calls textDocument/definition at the import position.
// Returns the resolved file path relative to root, or "" if external/unresolved.
func ResolveImport(client *Client, fileURI, rootDir string, loc ImportLocation) (string, error) {
	raw, err := client.Definition(context.Background(), fileURI, loc.Line, loc.Character)
	if err != nil {
		return "", err
	}

	if raw == nil || string(raw) == "null" {
		return "", nil
	}

	uri := extractTargetURI(raw)
	if uri == "" {
		return "", nil
	}

	return uriToRelPath(uri, rootDir), nil
}

func extractTargetURI(raw json.RawMessage) string {
	// Try []LocationLink first
	var links []locationLink
	if err := json.Unmarshal(raw, &links); err == nil && len(links) > 0 {
		return links[0].TargetURI
	}

	// Try []Location
	var locs []location
	if err := json.Unmarshal(raw, &locs); err == nil && len(locs) > 0 {
		return locs[0].URI
	}

	// Try single Location
	var loc location
	if err := json.Unmarshal(raw, &loc); err == nil && loc.URI != "" {
		return loc.URI
	}

	return ""
}

type locationLink struct {
	TargetURI   string   `json:"targetUri"`
	TargetRange LspRange `json:"targetRange"`
}

func uriToRelPath(uri, rootDir string) string {
	if !strings.HasPrefix(uri, "file://") {
		return ""
	}

	absPath := strings.TrimPrefix(uri, "file://")
	rel, err := filepath.Rel(rootDir, absPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "" // outside project root → external
	}
	return rel
}
