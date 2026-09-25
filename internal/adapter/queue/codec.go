package queue

import (
	"encoding/json"
	"errors"
	"io"
)

func ensureTaskJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return errors.New("task payload has trailing JSON")
}
