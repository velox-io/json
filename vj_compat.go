package vjson

import "encoding/json"

// encoding/json compatible type aliases.

type Number = json.Number
type RawMessage = json.RawMessage
type Marshaler = json.Marshaler
type Unmarshaler = json.Unmarshaler
