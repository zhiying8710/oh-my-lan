package client

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestSaveAndLoadState_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	want := &State{
		ServerURL:         "http://vps:8080",
		DeviceID:          "abc",
		DeviceName:        "macbook",
		TunnelSecret:      "secret",
		ServerFingerprint: "fp",
		ChiselAddr:        "vps:8443",
	}
	if err := SaveState(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if *got != *want {
		t.Errorf("got %+v want %+v", got, want)
	}
}

func TestLoadState_MissingReturnsErrStateMissing(t *testing.T) {
	_, err := LoadState(filepath.Join(t.TempDir(), "nope.json"))
	if !errors.Is(err, ErrStateMissing) {
		t.Errorf("got %v want ErrStateMissing", err)
	}
}

func TestLoadState_CorruptRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := SaveState(path, &State{ServerURL: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadState(path); err == nil {
		t.Error("缺 device_id 应失败")
	}
}
