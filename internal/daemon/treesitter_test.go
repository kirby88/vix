package daemon

import (
	"strings"
	"testing"
)

func TestCompressWithTreeSitterGo(t *testing.T) {
	src := `package main

// Add adds two integers and returns the result.
func Add(a, b int) int {
	// sum them up
	return a + b
}
`
	out, err := compressWithTreeSitter(src, "example.go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Fatal("expected non-empty compressed output for Go")
	}
	if len(out) >= len(src) {
		t.Errorf("compressed output (%d) should be shorter than input (%d)", len(out), len(src))
	}
	// Comments should be stripped
	if strings.Contains(out, "Add adds two") {
		t.Error("comment text should be stripped from compressed output")
	}
	// Key tokens should be preserved
	for _, tok := range []string{"func", "Add", "return"} {
		if !strings.Contains(out, tok) {
			t.Errorf("expected token %q in compressed output", tok)
		}
	}
}

func TestCompressWithTreeSitterPython(t *testing.T) {
	src := `class Calculator:
    """A simple calculator class."""

    def add(self, a, b):
        """Add two numbers."""
        return a + b

    def multiply(self, a, b):
        return a * b
`
	out, err := compressWithTreeSitter(src, "calc.py")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Fatal("expected non-empty compressed output for Python")
	}
	if len(out) >= len(src) {
		t.Errorf("compressed output (%d) should be shorter than input (%d)", len(out), len(src))
	}
	for _, tok := range []string{"class", "Calculator", "def", "add", "return"} {
		if !strings.Contains(out, tok) {
			t.Errorf("expected token %q in compressed output", tok)
		}
	}
}

func TestCompressWithTreeSitterJavaScript(t *testing.T) {
	src := `// Helper function
function greet(name) {
    const message = "Hello, " +
        name +
        "!";
    return message;
}
`
	out, err := compressWithTreeSitter(src, "greet.js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Fatal("expected non-empty compressed output for JavaScript")
	}
	if len(out) >= len(src) {
		t.Errorf("compressed output (%d) should be shorter than input (%d)", len(out), len(src))
	}
	for _, tok := range []string{"function", "greet", "return"} {
		if !strings.Contains(out, tok) {
			t.Errorf("expected token %q in compressed output", tok)
		}
	}
}

func TestCompressWithTreeSitterRust(t *testing.T) {
	src := `/// A point in 2D space.
// line comment
/* block comment */
struct Point {
    x: f64,
    y: f64,
}

impl Point {
    fn distance(&self, other: &Point) -> f64 {
        let dx = self.x - other.x;
        let dy = self.y - other.y;
        (dx * dx + dy * dy).sqrt()
    }
}
`
	out, err := compressWithTreeSitter(src, "point.rs")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Fatal("expected non-empty compressed output for Rust")
	}
	if len(out) >= len(src) {
		t.Errorf("compressed output (%d) should be shorter than input (%d)", len(out), len(src))
	}
	// Comments should be stripped
	for _, commentText := range []string{"A point in 2D space", "line comment", "block comment"} {
		if strings.Contains(out, commentText) {
			t.Errorf("comment text %q should be stripped from compressed output", commentText)
		}
	}
	for _, tok := range []string{"struct", "Point", "fn", "distance"} {
		if !strings.Contains(out, tok) {
			t.Errorf("expected token %q in compressed output", tok)
		}
	}
}

func TestCompressWithTreeSitterJava(t *testing.T) {
	src := `// line comment
/* block comment */
/** Javadoc comment */
class Greeter {
    String greet(String name) {
        return "Hello, " + name;
    }
}
`
	out, err := compressWithTreeSitter(src, "Greeter.java")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Fatal("expected non-empty compressed output for Java")
	}
	if len(out) >= len(src) {
		t.Errorf("compressed output (%d) should be shorter than input (%d)", len(out), len(src))
	}
	// Comments should be stripped
	for _, commentText := range []string{"line comment", "block comment", "Javadoc comment"} {
		if strings.Contains(out, commentText) {
			t.Errorf("comment text %q should be stripped from compressed output", commentText)
		}
	}
	for _, tok := range []string{"class", "Greeter", "return"} {
		if !strings.Contains(out, tok) {
			t.Errorf("expected token %q in compressed output", tok)
		}
	}
}

func TestCompressWithTreeSitterSwift(t *testing.T) {
	src := `// A greeting function
/* block comment */
func greet(name: String) -> String {
    let message = "Hello, " + name
    return message
}

class Greeter {
    var name: String

    init(name: String) {
        self.name = name
    }

    func greet() -> String {
        return "Hello, \(name)!"
    }
}
`
	out, err := compressWithTreeSitter(src, "Greeter.swift")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Fatal("expected non-empty compressed output for Swift")
	}
	if len(out) >= len(src) {
		t.Errorf("compressed output (%d) should be shorter than input (%d)", len(out), len(src))
	}
	// Comments should be stripped
	for _, commentText := range []string{"A greeting function", "block comment"} {
		if strings.Contains(out, commentText) {
			t.Errorf("comment text %q should be stripped from compressed output", commentText)
		}
	}
	for _, tok := range []string{"func", "greet", "class", "Greeter", "return"} {
		if !strings.Contains(out, tok) {
			t.Errorf("expected token %q in compressed output", tok)
		}
	}
}

func TestCompressWithTreeSitterKotlin(t *testing.T) {
	src := `// A greeting function
/* block comment */
fun greet(name: String): String {
    val message = "Hello, " + name
    return message
}

class Greeter(val name: String) {
    // greet method
    fun greet(): String {
        return "Hello, $name!"
    }
}
`
	out, err := compressWithTreeSitter(src, "Greeter.kt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Fatal("expected non-empty compressed output for Kotlin")
	}
	if len(out) >= len(src) {
		t.Errorf("compressed output (%d) should be shorter than input (%d)", len(out), len(src))
	}
	// Comments should be stripped
	for _, commentText := range []string{"A greeting function", "block comment", "greet method"} {
		if strings.Contains(out, commentText) {
			t.Errorf("comment text %q should be stripped from compressed output", commentText)
		}
	}
	for _, tok := range []string{"fun", "greet", "class", "Greeter", "return"} {
		if !strings.Contains(out, tok) {
			t.Errorf("expected token %q in compressed output", tok)
		}
	}
}

func TestCompressWithTreeSitterUnsupported(t *testing.T) {
	src := "Hello world, this is plain text."
	out, err := compressWithTreeSitter(src, "readme.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "" {
		t.Errorf("expected empty string for unsupported extension, got %q", out)
	}
}

func TestCompressWithTreeSitterEmpty(t *testing.T) {
	out, err := compressWithTreeSitter("", "empty.go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should not panic; result can be empty
	_ = out
}
