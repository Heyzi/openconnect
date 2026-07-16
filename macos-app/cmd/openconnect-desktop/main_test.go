package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAcquireInstanceRejectsSecondProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instance.lock")
	first, primary, err := acquireInstance(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if !primary {
		t.Fatal("first process did not acquire the instance lock")
	}
	const bootstrapURL = "http://127.0.0.1:32123/bootstrap?token=secret"
	info := instanceInfo{URL: bootstrapURL, Executable: "/Applications/OpenConnect Desktop.app/Contents/MacOS/openconnect-desktop", PID: os.Getpid()}
	if err := writeInstanceInfo(first, info); err != nil {
		t.Fatal(err)
	}

	second, primary, err := acquireInstance(path)
	if err != nil {
		t.Fatal(err)
	}
	if second != nil {
		second.Close()
		t.Fatal("second process unexpectedly acquired an instance file")
	}
	if primary {
		t.Fatal("second process was treated as the primary instance")
	}
	got, err := readInstanceInfo(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != info {
		t.Fatalf("instance info = %#v, want %#v", got, info)
	}
}

func TestReadLegacyInstanceURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instance.lock")
	const bootstrapURL = "http://127.0.0.1:32123/bootstrap?token=legacy"
	if err := os.WriteFile(path, []byte(bootstrapURL), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := readInstanceInfo(path)
	if err != nil || info.URL != bootstrapURL || info.Executable != "" || info.PID != 0 {
		t.Fatalf("legacy instance info = %#v, %v", info, err)
	}
}

func TestSameExecutable(t *testing.T) {
	const executable = "/Applications/OpenConnect Desktop.app/Contents/MacOS/openconnect-desktop"
	if !sameExecutable(executable, executable) || !sameExecutable(executable+" -no-browser", executable) {
		t.Fatal("same executable was not recognized")
	}
	if sameExecutable("/Users/me/Downloads/OpenConnect Desktop.app/Contents/MacOS/openconnect-desktop", executable) {
		t.Fatal("moved executable was treated as the current instance")
	}
}
