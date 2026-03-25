package daemon

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestMergeRanges(t *testing.T) {
	tests := []struct {
		name   string
		input  []readRange
		expect []readRange
	}{
		{
			name:   "single range",
			input:  []readRange{{1, 10}},
			expect: []readRange{{1, 10}},
		},
		{
			name:   "overlapping",
			input:  []readRange{{1, 10}, {5, 15}},
			expect: []readRange{{1, 15}},
		},
		{
			name:   "adjacent",
			input:  []readRange{{1, 10}, {11, 20}},
			expect: []readRange{{1, 20}},
		},
		{
			name:   "contained",
			input:  []readRange{{1, 100}, {10, 50}},
			expect: []readRange{{1, 100}},
		},
		{
			name:   "disjoint",
			input:  []readRange{{1, 10}, {20, 30}},
			expect: []readRange{{1, 10}, {20, 30}},
		},
		{
			name:   "multiple overlapping",
			input:  []readRange{{1, 5}, {3, 8}, {7, 12}, {20, 25}},
			expect: []readRange{{1, 12}, {20, 25}},
		},
		{
			name:   "unsorted",
			input:  []readRange{{20, 30}, {1, 10}},
			expect: []readRange{{1, 10}, {20, 30}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mergeRanges(tt.input)
			if len(result) != len(tt.expect) {
				t.Fatalf("got %d ranges, want %d: %v", len(result), len(tt.expect), result)
			}
			for i := range result {
				if result[i] != tt.expect[i] {
					t.Errorf("range[%d] = %v, want %v", i, result[i], tt.expect[i])
				}
			}
		})
	}
}

func TestIsCoveredByRanges(t *testing.T) {
	ranges := []readRange{{1, 50}, {100, 200}}

	tests := []struct {
		name    string
		r       readRange
		covered bool
	}{
		{"fully covered", readRange{1, 50}, true},
		{"subset", readRange{10, 30}, true},
		{"partially covered", readRange{40, 60}, false},
		{"not covered", readRange{60, 90}, false},
		{"second range covered", readRange{100, 150}, true},
		{"spans gap", readRange{40, 110}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isCoveredByRanges(ranges, tt.r)
			if got != tt.covered {
				t.Errorf("isCoveredByRanges(%v) = %v, want %v", tt.r, got, tt.covered)
			}
		})
	}
}

func TestReadTrackerRecordAndIsCovered(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "test.txt")
	os.WriteFile(file, []byte("line1\nline2\nline3\nline4\nline5\n"), 0644)

	rt := newReadTracker()

	// Not covered initially
	if rt.IsCovered(file, readRange{1, 5}) {
		t.Fatal("should not be covered before any record")
	}

	// Record lines 1-5
	rt.Record(file, readRange{1, math.MaxInt32}, 5)

	// Now covered
	if !rt.IsCovered(file, readRange{1, 5}) {
		t.Fatal("should be covered after recording 1-5")
	}
	if !rt.IsCovered(file, readRange{2, 4}) {
		t.Fatal("subset should be covered")
	}

	// Lines 6-10 not covered
	if rt.IsCovered(file, readRange{6, 10}) {
		t.Fatal("lines 6-10 should not be covered")
	}
}

func TestReadTrackerInvalidate(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "test.txt")
	os.WriteFile(file, []byte("hello\n"), 0644)

	rt := newReadTracker()
	rt.Record(file, readRange{1, 1}, 1)

	if !rt.IsCovered(file, readRange{1, 1}) {
		t.Fatal("should be covered after record")
	}

	rt.Invalidate(file)

	if rt.IsCovered(file, readRange{1, 1}) {
		t.Fatal("should not be covered after invalidate")
	}
}

func TestReadTrackerStaleMtime(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "test.txt")
	os.WriteFile(file, []byte("original\n"), 0644)

	rt := newReadTracker()
	rt.Record(file, readRange{1, 1}, 1)

	if !rt.IsCovered(file, readRange{1, 1}) {
		t.Fatal("should be covered initially")
	}

	// Modify the file — mtime changes
	os.WriteFile(file, []byte("modified content\n"), 0644)

	if rt.IsCovered(file, readRange{1, 1}) {
		t.Fatal("should not be covered after file modification")
	}
}

func TestDeduplicateBatch(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "test.txt")
	os.WriteFile(file, []byte("line1\nline2\nline3\n"), 0644)

	rt := newReadTracker()

	calls := []readFileCall{
		{ToolID: "t1", AbsPath: file, Range: readRange{1, 100}, Index: 0},
		{ToolID: "t2", AbsPath: file, Range: readRange{1, 50}, Index: 1},
		{ToolID: "t3", AbsPath: "/other/file.go", Range: readRange{1, math.MaxInt32}, Index: 2},
	}

	results := processReadFileDedup(rt, calls)

	// t1 should pass (first read of this file)
	if _, deduped := results["t1"]; deduped {
		t.Error("t1 should not be deduped (first read)")
	}

	// t2 should be deduped (same file, second read in batch)
	if dr, ok := results["t2"]; !ok {
		t.Error("t2 should be deduped (same file)")
	} else if !dr.IsError {
		t.Error("t2 dedup result should be an error")
	}

	// t3 should pass (different file)
	if _, deduped := results["t3"]; deduped {
		t.Error("t3 should not be deduped (different file)")
	}
}

func TestDeduplicateBatchWithHistory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "test.txt")
	os.WriteFile(file, []byte("line1\nline2\nline3\n"), 0644)

	rt := newReadTracker()
	rt.Record(file, readRange{1, 3}, 3)

	calls := []readFileCall{
		{ToolID: "t1", AbsPath: file, Range: readRange{1, 3}, Index: 0},
	}

	results := processReadFileDedup(rt, calls)

	// Should be deduped from history
	if dr, ok := results["t1"]; !ok {
		t.Error("t1 should be deduped from history")
	} else if !dr.IsError {
		t.Error("t1 dedup result should be an error")
	}
}

func TestSummarizeToolInputReadFile(t *testing.T) {
	tests := []struct {
		name   string
		input  map[string]any
		expect string
	}{
		{
			name:   "full file",
			input:  map[string]any{"path": "foo.go"},
			expect: "foo.go",
		},
		{
			name:   "with offset and limit",
			input:  map[string]any{"path": "foo.go", "offset": float64(10), "limit": float64(41)},
			expect: "foo.go:10-50",
		},
		{
			name:   "offset only",
			input:  map[string]any{"path": "foo.go", "offset": float64(10)},
			expect: "foo.go:10-",
		},
		{
			name:   "compress mode full file",
			input:  map[string]any{"path": "foo.go", "mode": "compress"},
			expect: "[compress] foo.go",
		},
		{
			name:   "original mode full file",
			input:  map[string]any{"path": "foo.go", "mode": "original"},
			expect: "foo.go",
		},
		{
			name:   "compress mode with offset and limit",
			input:  map[string]any{"path": "foo.go", "mode": "compress", "offset": float64(10), "limit": float64(41)},
			expect: "[compress] foo.go:10-50",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SummarizeToolInput("read_file", tt.input)
			if got != tt.expect {
				t.Errorf("got %q, want %q", got, tt.expect)
			}
		})
	}
}

func TestSummarizeToolInputEditFile(t *testing.T) {
	tests := []struct {
		name   string
		input  map[string]any
		expect string
	}{
		{
			name:   "same line count",
			input:  map[string]any{"path": "foo.go", "old_string": "a\nb\nc\nd\ne", "new_string": "1\n2\n3\n4\n5"},
			expect: "foo.go (5 lines changed)",
		},
		{
			name:   "net addition",
			input:  map[string]any{"path": "foo.go", "old_string": "a\nb\nc", "new_string": "1\n2\n3\n4\n5"},
			expect: "foo.go (3 lines changed, +2)",
		},
		{
			name:   "net deletion",
			input:  map[string]any{"path": "foo.go", "old_string": "a\nb\nc\nd\ne", "new_string": "1\n2\n3\n4"},
			expect: "foo.go (5 lines changed, -1)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SummarizeToolInput("edit_file", tt.input)
			if got != tt.expect {
				t.Errorf("got %q, want %q", got, tt.expect)
			}
		})
	}
}

func TestNormalizeRange(t *testing.T) {
	absPath, r := normalizeRange("/project", "src/main.go", map[string]any{})
	if absPath != "/project/src/main.go" {
		t.Errorf("absPath = %q, want /project/src/main.go", absPath)
	}
	if r.Start != 1 || r.End != math.MaxInt32 {
		t.Errorf("range = %v, want {1, MaxInt32}", r)
	}

	absPath, r = normalizeRange("/project", "/abs/path.go", map[string]any{
		"offset": float64(10),
		"limit":  float64(20),
	})
	if absPath != "/abs/path.go" {
		t.Errorf("absPath = %q, want /abs/path.go", absPath)
	}
	if r.Start != 10 || r.End != 29 {
		t.Errorf("range = %v, want {10, 29}", r)
	}
}

func TestCountLines(t *testing.T) {
	tests := []struct {
		input  string
		expect int
	}{
		{"", 0},
		{"hello", 1},
		{"hello\n", 2},
		{"a\nb\nc", 3},
	}
	for _, tt := range tests {
		got := countLines(tt.input)
		if got != tt.expect {
			t.Errorf("countLines(%q) = %d, want %d", tt.input, got, tt.expect)
		}
	}
}
