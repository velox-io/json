package bind

// Corpus fixtures live in the benchmark module (benchmark/corpus/testdata);
// this module reaches them over the filesystem.

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
)

func loadCorpus(name string) ([]byte, error) {
	gz, err := os.ReadFile(filepath.Join("..", "..", "benchmark", "corpus", "testdata", name+".json.gz"))
	if err != nil {
		return nil, err
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}
