package diag

import (
	"os"
	"strings"
	"sync"

	"cuelang.org/go/cue/token"
)

// FileCache is a concurrent-safe SourceCache that reads files from disk on
// first access and caches their split lines for subsequent lookups.
type FileCache struct {
	mu      sync.Mutex
	sources map[string][]string
}

// NewFileCache returns an empty FileCache.
func NewFileCache() *FileCache {
	return &FileCache{sources: make(map[string][]string)}
}

// Set primes the cache with the source bytes for filename, bypassing disk I/O
// on subsequent LineAt calls for that file.
func (c *FileCache) Set(filename string, source []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sources[filename] = strings.Split(string(source), "\n")
}

// LineAt returns the source line, 1-based line number, and 1-based column for
// pos. ok is false for invalid positions or unreadable files.
func (c *FileCache) LineAt(pos token.Pos) (line string, lineNum int, col int, ok bool) {
	if !pos.IsValid() {
		return "", 0, 0, false
	}
	filename := pos.Filename()
	lineNum = pos.Line()
	col = pos.Column()
	if filename == "" || lineNum <= 0 {
		return "", 0, 0, false
	}

	lines, loaded := c.load(filename)
	if !loaded || lineNum > len(lines) {
		return "", 0, 0, false
	}
	return lines[lineNum-1], lineNum, col, true
}

func (c *FileCache) load(filename string) ([]string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if lines, ok := c.sources[filename]; ok {
		return lines, true
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, false
	}
	lines := strings.Split(string(data), "\n")
	c.sources[filename] = lines
	return lines, true
}
