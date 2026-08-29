// Package corpus holds the shared JSON test corpora: one gzip-compressed
// document per dataset under testdata/, decompressed and cached on first
// use. The NDJSON log stream (log.json.zst) lives alongside and is
// embedded directly by the benchmark package.
package corpus

import (
	"bytes"
	"compress/gzip"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"sync"
)

//go:embed testdata/*.json.gz
var files embed.FS

var (
	mu    sync.Mutex
	cache = map[string][]byte{}
)

// Names returns every dataset name in lexical order.
func Names() []string {
	entries, err := fs.ReadDir(files, "testdata")
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), ".json.gz"); ok {
			out = append(out, name)
		}
	}
	return out
}

// Load returns the decompressed payload for a dataset, cached and read-only.
func Load(name string) ([]byte, error) {
	mu.Lock()
	defer mu.Unlock()
	if raw, ok := cache[name]; ok {
		return raw, nil
	}
	gz, err := files.ReadFile("testdata/" + name + ".json.gz")
	if err != nil {
		return nil, fmt.Errorf("unknown corpus dataset %q", name)
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, fmt.Errorf("corpus %q: %w", name, err)
	}
	raw, err := io.ReadAll(zr)
	zr.Close()
	if err != nil {
		return nil, fmt.Errorf("corpus %q: %w", name, err)
	}
	cache[name] = raw
	return raw, nil
}
