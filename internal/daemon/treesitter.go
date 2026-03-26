package daemon

import (
	"path/filepath"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_c "github.com/tree-sitter/tree-sitter-c/bindings/go"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tree_sitter_java "github.com/tree-sitter/tree-sitter-java/bindings/go"
	tree_sitter_javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tree_sitter_rust "github.com/tree-sitter/tree-sitter-rust/bindings/go"
)

// languageMap maps file extensions to Tree-sitter language constructors
var languageMap map[string]func() *sitter.Language

func init() {
	goLang := sitter.NewLanguage(tree_sitter_go.Language())
	jsLang := sitter.NewLanguage(tree_sitter_javascript.Language())
	pyLang := sitter.NewLanguage(tree_sitter_python.Language())
	rsLang := sitter.NewLanguage(tree_sitter_rust.Language())
	cLang := sitter.NewLanguage(tree_sitter_c.Language())
	javaLang := sitter.NewLanguage(tree_sitter_java.Language())

	languageMap = map[string]func() *sitter.Language{
		".go":   func() *sitter.Language { return goLang },
		".js":   func() *sitter.Language { return jsLang },
		".jsx":  func() *sitter.Language { return jsLang },
		".ts":   func() *sitter.Language { return jsLang },
		".tsx":  func() *sitter.Language { return jsLang },
		".py":   func() *sitter.Language { return pyLang },
		".rs":   func() *sitter.Language { return rsLang },
		".c":    func() *sitter.Language { return cLang },
		".cpp":  func() *sitter.Language { return cLang },
		".h":    func() *sitter.Language { return cLang },
		".hpp":  func() *sitter.Language { return cLang },
		".java": func() *sitter.Language { return javaLang },
	}
}

// newParserForFile creates a new Tree-sitter parser for the given file.
// Each call returns a fresh parser, safe for concurrent use across goroutines.
func newParserForFile(filePath string) *sitter.Parser {
	ext := strings.ToLower(filepath.Ext(filePath))
	langFn, ok := languageMap[ext]
	if !ok {
		return nil
	}
	p := sitter.NewParser()
	p.SetLanguage(langFn())
	return p
}

// languageUsesSemicolons returns true if the language typically uses semicolons as statement terminators
func languageUsesSemicolons(filePath string) bool {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".js", ".jsx", ".ts", ".tsx", ".c", ".cpp", ".h", ".hpp", ".java", ".rs", ".go":
		return true
	case ".py":
		return false
	default:
		return false
	}
}

// compressWithTreeSitter uses Tree-sitter to parse and compress source code
func compressWithTreeSitter(content string, filePath string) (string, error) {
	parser := newParserForFile(filePath)
	if parser == nil {
		// Unsupported language, return error to trigger fallback
		return "", nil
	}
	defer parser.Close()

	// Parse the content
	tree := parser.Parse([]byte(content), nil)
	if tree == nil || tree.RootNode() == nil {
		// Parsing failed, return error to trigger fallback
		return "", nil
	}
	defer tree.Close()

	rootNode := tree.RootNode()
	
	// Extract compressed source by walking the tree
	var result strings.Builder
	walkNode(rootNode, []byte(content), &result, filePath)
	
	return strings.TrimSpace(result.String()), nil
}

// walkNode recursively walks the syntax tree and builds compressed output
func walkNode(node *sitter.Node, source []byte, result *strings.Builder, filePath string) {
	if node == nil {
		return
	}

	nodeType := node.Kind()
	
	// Skip pure whitespace nodes and comments (all grammar variants across supported languages)
	switch nodeType {
	case "\n", "\t", " ",
		"comment",       // Go, Python, JavaScript, C
		"line_comment",  // Rust, Java
		"block_comment": // Rust, Java
		return
	}

	// If this is a leaf node (no children), add its text
	if node.ChildCount() == 0 {
		text := node.Utf8Text(source)
		if text != "" && text != "\n" && text != "\t" {
			result.WriteString(text)
			result.WriteString(" ")
		}
		return
	}

	// For statement nodes, process children and potentially add semicolon
	isStatement := strings.Contains(nodeType, "statement") || 
		strings.Contains(nodeType, "declaration") ||
		nodeType == "expression_statement" ||
		nodeType == "variable_declaration" ||
		nodeType == "return_statement" ||
		nodeType == "if_statement" ||
		nodeType == "for_statement" ||
		nodeType == "while_statement"

	// Process all children
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		walkNode(child, source, result, filePath)
	}

	// Add semicolon after statements if the language uses them
	if isStatement && languageUsesSemicolons(filePath) {
		currentText := result.String()
		if len(currentText) > 0 {
			lastChar := currentText[len(currentText)-1]
			// Don't add semicolon if already present or if ends with brace
			if lastChar != ';' && lastChar != '{' && lastChar != '}' && lastChar != ' ' {
				result.WriteString("; ")
			}
		}
	}
}
