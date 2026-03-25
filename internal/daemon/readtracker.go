package daemon

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// readRange represents a 1-based inclusive line range.
type readRange struct {
	Start int
	End   int
}

// fileHistory tracks which line ranges have been read for a file.
type fileHistory struct {
	ranges  []readRange
	mtimeNs int64
	size    int64
}

// readTracker tracks read_file calls to deduplicate across a session.
type readTracker struct {
	mu      sync.Mutex
	history map[string]*fileHistory
}

func newReadTracker() *readTracker {
	return &readTracker{
		history: make(map[string]*fileHistory),
	}
}

// normalizeRange converts tool params (offset, limit) to an absolute path and readRange.
// No offset/limit means full file: {1, math.MaxInt32}.
func normalizeRange(cwd, path string, input map[string]any) (string, readRange) {
	absPath := path
	if !filepath.IsAbs(path) {
		absPath = filepath.Join(cwd, path)
	}

	start := 1
	end := math.MaxInt32

	if off, ok := input["offset"].(float64); ok && off > 0 {
		start = int(off)
	}
	if lim, ok := input["limit"].(float64); ok && lim > 0 {
		end = start + int(lim) - 1
	}

	return absPath, readRange{Start: start, End: end}
}

// IsCovered checks if the given range is fully covered by previously recorded ranges.
// It also checks file mtime/size and auto-invalidates if the file has changed.
func (rt *readTracker) IsCovered(absPath string, r readRange) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	fh, ok := rt.history[absPath]
	if !ok {
		return false
	}

	// Check if file has changed on disk
	info, err := os.Stat(absPath)
	if err != nil {
		// File gone or unreadable — invalidate
		delete(rt.history, absPath)
		return false
	}
	if info.ModTime().UnixNano() != fh.mtimeNs || info.Size() != fh.size {
		delete(rt.history, absPath)
		return false
	}

	return isCoveredByRanges(fh.ranges, r)
}

// isCoveredByRanges checks if r is fully covered by sorted, non-overlapping ranges.
func isCoveredByRanges(ranges []readRange, r readRange) bool {
	pos := r.Start
	for _, rng := range ranges {
		if rng.Start > pos {
			return false
		}
		if rng.End >= pos {
			pos = rng.End + 1
		}
		if pos > r.End {
			return true
		}
	}
	return pos > r.End
}

// Record records a successfully read range. Clamps sentinel End to actualEnd.
// Merges overlapping/adjacent ranges.
func (rt *readTracker) Record(absPath string, r readRange, actualEnd int) {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	// Clamp sentinel
	if r.End == math.MaxInt32 && actualEnd > 0 {
		r.End = actualEnd
	}

	fh, ok := rt.history[absPath]
	if !ok {
		fh = &fileHistory{}
		rt.history[absPath] = fh
	}

	// Update file metadata
	if info, err := os.Stat(absPath); err == nil {
		fh.mtimeNs = info.ModTime().UnixNano()
		fh.size = info.Size()
	}

	fh.ranges = append(fh.ranges, r)
	fh.ranges = mergeRanges(fh.ranges)
}

// Invalidate clears all recorded ranges for a file.
func (rt *readTracker) Invalidate(absPath string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	delete(rt.history, absPath)
}

// mergeRanges sorts and merges overlapping/adjacent ranges.
func mergeRanges(ranges []readRange) []readRange {
	if len(ranges) <= 1 {
		return ranges
	}

	sort.Slice(ranges, func(i, j int) bool {
		return ranges[i].Start < ranges[j].Start
	})

	merged := []readRange{ranges[0]}
	for _, r := range ranges[1:] {
		last := &merged[len(merged)-1]
		if r.Start <= last.End+1 {
			// Overlapping or adjacent — extend
			if r.End > last.End {
				last.End = r.End
			}
		} else {
			merged = append(merged, r)
		}
	}
	return merged
}

// readFileCall represents a read_file tool call for batch dedup.
type readFileCall struct {
	ToolID  string
	AbsPath string
	Range   readRange
	Index   int
}

// dedupResult holds the result of a deduped read_file call.
type dedupResult struct {
	Output  string
	IsError bool
}

// processReadFileDedup checks read_file tool calls against the tracker.
// It returns a map of toolID -> dedupResult for calls that should be skipped.
// Calls not in the returned map should be executed normally.
func processReadFileDedup(
	tracker *readTracker,
	calls []readFileCall,
) map[string]dedupResult {
	results := make(map[string]dedupResult)

	// Batch dedup: if multiple calls read the same file in one response,
	// only the first one should proceed; subsequent ones are dupes.
	seen := make(map[string]bool) // absPath -> already seen in this batch
	for _, call := range calls {
		if seen[call.AbsPath] {
			results[call.ToolID] = dedupResult{
				Output:  formatDedupError(call.AbsPath, call.Range),
				IsError: true,
			}
			continue
		}

		// Check history
		if tracker.IsCovered(call.AbsPath, call.Range) {
			results[call.ToolID] = dedupResult{
				Output:  formatDedupError(call.AbsPath, call.Range),
				IsError: true,
			}
			continue
		}

		seen[call.AbsPath] = true
	}

	return results
}

// countLines counts the number of lines in a string.
func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := 1
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			n++
		}
	}
	return n
}

func formatDedupError(absPath string, r readRange) string {
	if r.End == math.MaxInt32 {
		return "Duplicate read: " + absPath + " was already read. Do not re-read files you have already seen in this conversation."
	}
	return fmt.Sprintf("Duplicate read: %s lines %d-%d was already read. Do not re-read files you have already seen in this conversation.", absPath, r.Start, r.End)
}
