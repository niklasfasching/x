package util

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var updateSnapshots = flag.Bool("update-snapshots", false, "update testdata snapshots")

type SnapMarshaller interface {
	MarshalSnap() (string, string, error)
}

func Snapshot[V any](t *testing.T, v V) {
	p, actual, ext := filepath.Join("testdata", t.Name()), "", ".json"
	if m, ok := any(v).(SnapMarshaller); ok {
		s, e, err := m.MarshalSnap()
		if err != nil {
			t.Fatalf("failed to marshal snapshot: %s (%v)", err, v)
		}
		actual, ext = s, e
	} else {
		bs, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			t.Fatalf("failed to marshal snapshot: %s (%v)", err, v)
		}
		actual = string(bs)
	}
	if *updateSnapshots {
		if err := os.MkdirAll("testdata", 0755); err != nil {
			t.Fatalf("failed to create testdata: %s", err)
		} else if err := os.WriteFile(p+ext, []byte(actual), 0644); err != nil {
			t.Fatalf("failed to write snapshot: %s", err)
		}
	} else if bs, err := os.ReadFile(p + ext); err != nil && !os.IsNotExist(err) {
		t.Fatalf("failed to read snapshot: %s", err)
	} else if expected := string(bs); actual != expected {
		t.Fatalf("snapshot does not match (actual != expected):\n%q\n----------\n%q", actual, expected)
	}
}
