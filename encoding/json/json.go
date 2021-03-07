// +build !jsoniter,!gojay,!go-json

package json

import "encoding/json"

var (
	MarshalIndent = json.MarshalIndent
	Marshal       = json.Marshal
	Unmarshal     = json.Unmarshal
	NewDecoder    = json.NewDecoder
	NewEncoder    = json.NewEncoder
)
