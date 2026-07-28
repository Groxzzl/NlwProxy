package proxyimport

import (
	"encoding/json"
	"strings"
)

// unmarshalJSON is a generic JSON unmarshaller. It does NOT disallow
// unknown fields so that sources like monosans that send rich metadata
// alongside the fields we care about still parse correctly.
func unmarshalJSON[T any](body string) (T, error) {
	var zero T
	dec := json.NewDecoder(strings.NewReader(body))
	// Intentionally NOT using DisallowUnknownFields — many sources
	// (e.g. monosans/proxy-list) include extra metadata fields.
	err := dec.Decode(&zero)
	if err != nil {
		return zero, err
	}
	return zero, nil
}
