package cninetwork

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
)

func Test_isCNIResultForPID_Found(t *testing.T) {
	body := `nats-621
eth1`
	fileName := `10.63.0.2`
	container := "nats"
	PID := uint32(621)
	fullPath := filepath.Join(os.TempDir(), fileName)

	err := ioutil.WriteFile(fullPath, []byte(body), 0700)
	if err != nil {
		t.Fatalf(err.Error())
	}
	defer func() {
		os.Remove(fullPath)
	}()

	got, err := isCNIResultForPID(fullPath, container, PID)

	if err != nil {
		t.Fatalf(err.Error())
	}

	want := true
	if got != want {
		t.Fatalf("want %v, but got %v", want, got)
	}
}

func Test_isCNIResultForPID_NoMatch(t *testing.T) {
	body := `nats-621
eth1`
	fileName := `10.63.0.3`
	container := "gateway"
	PID := uint32(621)
	fullPath := filepath.Join(os.TempDir(), fileName)

	err := ioutil.WriteFile(fullPath, []byte(body), 0700)
	if err != nil {
		t.Fatalf(err.Error())
	}
	defer func() {
		os.Remove(fullPath)
	}()

	got, err := isCNIResultForPID(fullPath, container, PID)

	if err != nil {
		t.Fatalf(err.Error())
	}
	want := false
	if got != want {
		t.Fatalf("want %v, but got %v", want, got)
	}
}
