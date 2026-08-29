package jsonbench

import (
	"encoding/json"
	"fmt"
	"slices"

	"dev.local/benchmark/corpus"
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

// DatasetNames returns all supported dataset names.
func DatasetNames() []string {
	out := make([]string, len(allDatasetNames))
	copy(out, allDatasetNames)
	return out
}

// LoadDatasetJSON returns the decompressed JSON payload for a benchmark
// dataset, served from the shared corpus. The returned bytes are cached
// and should be treated as read-only.
func LoadDatasetJSON(name string) ([]byte, error) {
	if slices.Contains(allDatasetNames, name) {
		return corpus.Load(name)
	}
	return nil, fmt.Errorf("unknown dataset %q", name)
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
