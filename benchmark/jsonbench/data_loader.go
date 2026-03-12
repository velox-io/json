package jsonbench

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

const (
	DatasetCanadaGeometry = "canada_geometry"
	DatasetCITMCatalog    = "citm_catalog"
	DatasetGolangSource   = "golang_source"
	DatasetStringUnicode  = "string_unicode"
	DatasetSyntheaFHIR    = "synthea_fhir"
	DatasetTwitterStatus  = "twitter_status"
)

var allDatasetNames = []string{
	DatasetCanadaGeometry,
	DatasetCITMCatalog,
	DatasetGolangSource,
	DatasetStringUnicode,
	DatasetSyntheaFHIR,
	DatasetTwitterStatus,
}

//go:embed testdata/canada_geometry.json.gz
var canadaGeometryJSONGZ []byte

//go:embed testdata/citm_catalog.json.gz
var citmCatalogJSONGZ []byte

//go:embed testdata/golang_source.json.gz
var golangSourceJSONGZ []byte

//go:embed testdata/string_unicode.json.gz
var stringUnicodeJSONGZ []byte

//go:embed testdata/synthea_fhir.json.gz
var syntheaFHIRJSONGZ []byte

//go:embed testdata/twitter_status.json.gz
var twitterStatusJSONGZ []byte

type rawDataset struct {
	gz   []byte
	once sync.Once
	raw  []byte
	err  error
}

var datasetByName = map[string]*rawDataset{
	DatasetCanadaGeometry: {gz: canadaGeometryJSONGZ},
	DatasetCITMCatalog:    {gz: citmCatalogJSONGZ},
	DatasetGolangSource:   {gz: golangSourceJSONGZ},
	DatasetStringUnicode:  {gz: stringUnicodeJSONGZ},
	DatasetSyntheaFHIR:    {gz: syntheaFHIRJSONGZ},
	DatasetTwitterStatus:  {gz: twitterStatusJSONGZ},
}

// DatasetNames returns all supported dataset names.
func DatasetNames() []string {
	out := make([]string, len(allDatasetNames))
	copy(out, allDatasetNames)
	return out
}

// LoadDatasetJSON returns the decompressed JSON payload for a dataset.
//
// The returned bytes are cached and should be treated as read-only.
func LoadDatasetJSON(name string) ([]byte, error) {
	ds, ok := datasetByName[name]
	if !ok {
		return nil, fmt.Errorf("unknown dataset %q", name)
	}

	ds.once.Do(func() {
		ds.raw, ds.err = gunzip(ds.gz)
	})
	if ds.err != nil {
		return nil, fmt.Errorf("load dataset %q: %w", name, ds.err)
	}
	return ds.raw, nil
}

// UnmarshalDataset unmarshals a dataset into dst.
func UnmarshalDataset(name string, dst any) error {
	data, err := LoadDatasetJSON(name)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("unmarshal dataset %q: %w", name, err)
	}
	return nil
}

// LoadDataset loads and unmarshals a dataset into a new typed value.
func LoadDataset[T any](name string) (*T, error) {
	var out T
	if err := UnmarshalDataset(name, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func LoadCanadaGeometryJSON() ([]byte, error) { return LoadDatasetJSON(DatasetCanadaGeometry) }
func LoadCITMCatalogJSON() ([]byte, error)    { return LoadDatasetJSON(DatasetCITMCatalog) }
func LoadGolangSourceJSON() ([]byte, error)   { return LoadDatasetJSON(DatasetGolangSource) }
func LoadStringUnicodeJSON() ([]byte, error)  { return LoadDatasetJSON(DatasetStringUnicode) }
func LoadSyntheaFHIRJSON() ([]byte, error)    { return LoadDatasetJSON(DatasetSyntheaFHIR) }
func LoadTwitterStatusJSON() ([]byte, error)  { return LoadDatasetJSON(DatasetTwitterStatus) }

func LoadCanadaGeometry() (*CanadaRoot, error) { return LoadDataset[CanadaRoot](DatasetCanadaGeometry) }
func LoadCITMCatalog() (*CITMRoot, error)      { return LoadDataset[CITMRoot](DatasetCITMCatalog) }
func LoadGolangSource() (*GolangRoot, error)   { return LoadDataset[GolangRoot](DatasetGolangSource) }
func LoadStringUnicode() (*StringRoot, error)  { return LoadDataset[StringRoot](DatasetStringUnicode) }
func LoadSyntheaFHIR() (*SyntheaRoot, error)   { return LoadDataset[SyntheaRoot](DatasetSyntheaFHIR) }
func LoadTwitterStatus() (*TwitterRoot, error) { return LoadDataset[TwitterRoot](DatasetTwitterStatus) }

func gunzip(src []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(src))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}
