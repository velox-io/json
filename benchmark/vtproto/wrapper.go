//go:build bincodec

package vtproto

import (
	"encoding/json"
	"sync"
)

var (
	podsWireOnce sync.Once
	podsWireData []byte
	podsWireJSON int
)

func LoadKubePodsWire(data []byte) ([]byte, int) {
	podsWireOnce.Do(func() {
		var v KubePodList
		if err := json.Unmarshal(data, &v); err != nil {
			panic("vtproto: load pods json: " + err.Error())
		}
		wire, err := v.MarshalVT()
		if err != nil {
			panic("vtproto: marshal pods: " + err.Error())
		}
		podsWireData = wire
		podsWireJSON = len(data)
	})
	return podsWireData, podsWireJSON
}

func UnmarshalKubePodsVT(data []byte) error {
	var v KubePodList
	return v.UnmarshalVT(data)
}

func UnmarshalKubePodsVTUnsafe(data []byte) error {
	var v KubePodList
	return v.UnmarshalVTUnsafe(data)
}
