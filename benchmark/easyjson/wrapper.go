package easyjson

import (
	"encoding/json"
	"sync"

	ej "github.com/mailru/easyjson"
)

// Marshal wrappers — each caches the decoded value on first call,
// then returns easyjson.Marshal result on every call.

var (
	tinyOnce  sync.Once
	tinyValue Tiny

	smallOnce  sync.Once
	smallValue Book

	escapeHeavyOnce  sync.Once
	escapeHeavyValue EscapeHeavyPayload

	podsOnce  sync.Once
	podsValue KubePodList

	twitterOnce  sync.Once
	twitterValue TwitterStruct
)

func initTiny(data []byte) {
	tinyOnce.Do(func() {
		if err := json.Unmarshal(data, &tinyValue); err != nil {
			panic("easyjson: load tiny: " + err.Error())
		}
	})
}

func initSmall(data []byte) {
	smallOnce.Do(func() {
		if err := json.Unmarshal(data, &smallValue); err != nil {
			panic("easyjson: load small: " + err.Error())
		}
	})
}

func initEscapeHeavy(data []byte) {
	escapeHeavyOnce.Do(func() {
		if err := json.Unmarshal(data, &escapeHeavyValue); err != nil {
			panic("easyjson: load escape_heavy: " + err.Error())
		}
	})
}

func initPods(data []byte) {
	podsOnce.Do(func() {
		if err := json.Unmarshal(data, &podsValue); err != nil {
			panic("easyjson: load pods: " + err.Error())
		}
	})
}

func initTwitter(data []byte) {
	twitterOnce.Do(func() {
		if err := json.Unmarshal(data, &twitterValue); err != nil {
			panic("easyjson: load twitter: " + err.Error())
		}
	})
}

// MarshalTiny marshals a Tiny value using easyjson.
// data is used on first call to populate the cached value.
func MarshalTiny(data []byte) ([]byte, error) {
	initTiny(data)
	return ej.Marshal(&tinyValue)
}

// MarshalSmall marshals a Book value using easyjson.
func MarshalSmall(data []byte) ([]byte, error) {
	initSmall(data)
	return ej.Marshal(&smallValue)
}

// MarshalEscapeHeavy marshals an EscapeHeavyPayload using easyjson.
func MarshalEscapeHeavy(data []byte) ([]byte, error) {
	initEscapeHeavy(data)
	return ej.Marshal(&escapeHeavyValue)
}

// MarshalKubePods marshals a KubePodList using easyjson.
func MarshalKubePods(data []byte) ([]byte, error) {
	initPods(data)
	return ej.Marshal(&podsValue)
}

// MarshalTwitter marshals a TwitterStruct using easyjson.
func MarshalTwitter(data []byte) ([]byte, error) {
	initTwitter(data)
	return ej.Marshal(&twitterValue)
}

// Unmarshal wrappers — each creates a fresh local value and unmarshals into it.

func UnmarshalTiny(data []byte) error {
	var v Tiny
	return ej.Unmarshal(data, &v)
}

func UnmarshalSmall(data []byte) error {
	var v Book
	return ej.Unmarshal(data, &v)
}

func UnmarshalEscapeHeavy(data []byte) error {
	var v EscapeHeavyPayload
	return ej.Unmarshal(data, &v)
}

func UnmarshalKubePods(data []byte) error {
	var v KubePodList
	return ej.Unmarshal(data, &v)
}

func UnmarshalTwitter(data []byte) error {
	var v TwitterStruct
	return ej.Unmarshal(data, &v)
}
