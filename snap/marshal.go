package snap

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type JSON struct{}

type TXT struct{}

func (JSON) Marshal(v any) ([]byte, error) {
	bs, err := json.MarshalIndent(v, "", "  ")
	return bytes.ReplaceAll(bs, []byte("\\n"), []byte("\n\t")), err
}

func (JSON) Unmarshal(bs []byte, v any) error {
	return json.Unmarshal(bytes.ReplaceAll(bs, []byte("\n\t"), []byte("\\n")), v)
}

func (JSON) Ext() string { return ".json" }

func (TXT) Marshal(v any) ([]byte, error) {
	switch v := v.(type) {
	case []byte:
		return v, nil
	case string:
		return []byte(v), nil
	default:
		return nil, fmt.Errorf("failed to marshal non-string value: %v", v)
	}
}

func (TXT) Unmarshal(bs []byte, v any) error {
	switch v := v.(type) {
	case *[]byte:
		*v = bs
		return nil
	case *string:
		*v = string(bs)
		return nil
	default:
		return fmt.Errorf("failed to unmarshal non-string value: %v", v)
	}
}

func (TXT) Ext() string { return ".txt" }
