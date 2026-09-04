package snapshot

import (
	"encoding/json"
	"time"

	"airbg.org/internal/store"
)

// WindPayloadJSONForTesting exposes the unexported payload to the external test
// package as the bytes a client would receive.
func WindPayloadJSONForTesting(now, validAt time.Time, model string, resDeg float64, vs []store.WindVector) []byte {
	b, err := json.Marshal(windPayloadFrom(now, validAt, model, resDeg, vs))
	if err != nil {
		panic(err)
	}
	return b
}
