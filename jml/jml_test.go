package jml

import (
	"encoding/json"
	"testing"

	"github.com/niklasfasching/x/snap"
)

func RoundTrip(t *testing.T, bs []byte) {
	v, asJSON, failed := interface{}(nil), "", false
	if err := Unmarshal(bs, &v); err != nil {
		asJSON, failed = "jml unmarshal: "+err.Error(), true
	} else if bs, err := json.MarshalIndent(v, "", "  "); err != nil {
		asJSON, failed = "json marshal: "+err.Error(), true
	} else {
		asJSON = string(bs)
	}
	snap.Snap(t, snap.TXT{}, ".json", asJSON)
	if failed {
		return
	}
	bs, err := Marshal(v)
	asJML := string(bs)
	if err != nil {
		asJML = "jml marshal: " + err.Error()
	}
	snap.Snap(t, snap.TXT{}, ".yml", asJML)
}

func TestCases(t *testing.T) {
	snap.Cases(t, "*case.yaml", func(t *testing.T, name string, bs []byte) {
		RoundTrip(t, bs)
	})
}
