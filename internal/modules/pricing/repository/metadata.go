package repository

import (
	"encoding/json"

	"github.com/bdrtr/gobit/core/errors"
)

// codeMetadataInvalid reports a jsonb column this module could not read or
// write.
//
// It is INTERNAL rather than invalid on the way out and INVALID on the way in,
// and the split is the same one the order module makes: a value that will not
// decode came out of the database, which is this system's fault, while a value
// that will not encode came from the caller.
const codeMetadataInvalid = "pricing_metadata_invalid"

// toJSONMap converts a jsonb column to a map.
//
// An empty or JSON null value returns a nil map, so the field does not appear
// in the API response at all rather than appearing as "metadata": null.
func toJSONMap(raw []byte) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, codeMetadataInvalid,
			"the JSON field could not be decoded")
	}
	if len(out) == 0 {
		return nil, nil
	}

	return out, nil
}

// fromJSONMap converts a map to the bytes written into a jsonb column.
//
// A nil map becomes an empty object: the column is NOT NULL, and the difference
// between "nothing was written" and "an empty object was written" means nothing
// to a field this module never reads.
func fromJSONMap(m map[string]any) ([]byte, error) {
	if len(m) == 0 {
		return []byte("{}"), nil
	}

	raw, err := json.Marshal(m)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInvalid, codeMetadataInvalid,
			"the JSON field could not be encoded")
	}

	return raw, nil
}
