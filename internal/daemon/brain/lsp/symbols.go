package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

// Symbol represents an LSP DocumentSymbol converted to a flat struct.
type Symbol struct {
	Name      string
	Kind      string // LSP SymbolKind name: "Function", "Method", "Class", "Struct", etc.
	StartLine int    // 1-based
	EndLine   int    // 1-based
	Detail    string // type signature if LSP provides it
	Parent    string // parent symbol name for nested symbols
}

// SymbolKind int → string mapping from LSP spec.
var symbolKindName = map[int]string{
	1: "File", 2: "Module", 3: "Namespace", 4: "Package",
	5: "Class", 6: "Method", 7: "Property", 8: "Field",
	9: "Constructor", 10: "Enum", 11: "Interface", 12: "Function",
	13: "Variable", 14: "Constant", 15: "String", 16: "Number",
	17: "Boolean", 18: "Array", 19: "Object", 20: "Key",
	21: "Null", 22: "EnumMember", 23: "Struct", 24: "Event",
	25: "Operator", 26: "TypeParameter",
}

// Kinds to skip — struct fields that would clutter the symbol list.
var skipKinds = map[int]bool{
	7: true, // Property
	8: true, // Field
}

// documentSymbol is the LSP DocumentSymbol (hierarchical form).
type documentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail"`
	Kind           int              `json:"kind"`
	Range          LspRange         `json:"range"`
	SelectionRange LspRange         `json:"selectionRange"`
	Children       []documentSymbol `json:"children"`
}

// symbolInformation is the LSP SymbolInformation (flat form).
type symbolInformation struct {
	Name     string   `json:"name"`
	Kind     int      `json:"kind"`
	Location location `json:"location"`
}

// LspRange represents an LSP range with start and end Positions.
type LspRange struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Position represents a Position in a text document (0-based line and character).
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type location struct {
	URI   string   `json:"uri"`
	Range LspRange `json:"range"`
}

// ExtractSymbols calls documentSymbol on an LSP client and returns flat symbols.
func ExtractSymbols(client *Client, filePath, relPath, languageID string) ([]Symbol, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	uri := "file://" + filePath

	if err := client.DidOpen(uri, languageID, string(content)); err != nil {
		return nil, fmt.Errorf("didOpen: %w", err)
	}
	defer client.DidClose(uri)

	raw, err := client.DocumentSymbol(context.Background(), uri)
	if err != nil {
		return nil, fmt.Errorf("documentSymbol: %w", err)
	}

	if raw == nil || string(raw) == "null" {
		return nil, nil
	}

	// Try hierarchical DocumentSymbol[] first
	var docSymbols []documentSymbol
	if err := json.Unmarshal(raw, &docSymbols); err == nil && len(docSymbols) > 0 {
		var symbols []Symbol
		flattenDocSymbols(docSymbols, "", &symbols)
		return symbols, nil
	}

	// Fall back to flat SymbolInformation[]
	var symInfos []symbolInformation
	if err := json.Unmarshal(raw, &symInfos); err == nil && len(symInfos) > 0 {
		var symbols []Symbol
		for _, si := range symInfos {
			if skipKinds[si.Kind] {
				continue
			}
			symbols = append(symbols, Symbol{
				Name:      si.Name,
				Kind:      SymbolKindName(si.Kind),
				StartLine: si.Location.Range.Start.Line + 1,
				EndLine:   si.Location.Range.End.Line + 1,
			})
		}
		return symbols, nil
	}

	return nil, nil
}

func flattenDocSymbols(symbols []documentSymbol, parent string, out *[]Symbol) {
	for _, s := range symbols {
		if skipKinds[s.Kind] {
			continue
		}
		sym := Symbol{
			Name:      s.Name,
			Kind:      SymbolKindName(s.Kind),
			StartLine: s.Range.Start.Line + 1,
			EndLine:   s.Range.End.Line + 1,
			Detail:    s.Detail,
			Parent:    parent,
		}
		*out = append(*out, sym)

		if len(s.Children) > 0 {
			flattenDocSymbols(s.Children, s.Name, out)
		}
	}
}

// SymbolKindName returns the human-readable name for an LSP SymbolKind integer.
func SymbolKindName(kind int) string {
	if name, ok := symbolKindName[kind]; ok {
		return name
	}
	return fmt.Sprintf("Unknown(%d)", kind)
}
