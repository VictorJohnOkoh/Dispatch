package main

import (
	"path/filepath"
	"testing"
)

func TestRegistrationLockIsReleasedWhenHandleCloses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	first, err := lockRegistration(path)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := lockRegistration(path); err == nil {
		second.Close()
		t.Fatal("second owner acquired lock")
	}
	first.Close()
	third, err := lockRegistration(path)
	if err != nil {
		t.Fatal(err)
	}
	third.Close()
}
