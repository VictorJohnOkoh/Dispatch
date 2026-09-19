package main

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
	"golang.org/x/crypto/ssh"
)

func TestRegistrationPermissionsLinux(t *testing.T) {
	for _, bad := range []string{"", "home-write", "directory-write", "file-write", "directory-link", "file-link"} {
		t.Run(bad, func(t *testing.T) {
			home := t.TempDir()
			dir := filepath.Join(home, ".ssh")
			file := filepath.Join(dir, "authorized_keys")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte("# existing key\n"), 0600); err != nil {
				t.Fatal(err)
			}
			var err error
			switch bad {
			case "home-write":
				err = os.Chmod(home, 0770)
			case "directory-write":
				err = os.Chmod(dir, 0770)
			case "file-write":
				err = os.Chmod(file, 0660)
			case "directory-link":
				err = os.Rename(dir, dir+"-target")
				if err == nil {
					err = os.Symlink(dir+"-target", dir)
				}
			case "file-link":
				err = os.Rename(file, file+"-target")
				if err == nil {
					err = os.Symlink(file+"-target", file)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			err = checkRegistrationPermissions(home)
			if (err != nil) != (bad != "") {
				t.Fatalf("permissions check: %v", err)
			}
		})
	}
}

func TestRegistrationCommandLinuxPreservesExecutablePath(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "dispatch 'quoted' \"double\" $HOME `id`")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	k := &registrationKeys{path: filepath.Join(dir, "authorized_keys"), exe: exe, port: 7717}
	c, err := protocol.NewRegistrationCode("127.0.0.1:22", "localuser", "SHA256:"+base64.RawStdEncoding.EncodeToString(make([]byte, 32)), 7717, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Temporary(c); err != nil {
		t.Fatal(err)
	}
	line, err := os.ReadFile(k.path)
	if err != nil {
		t.Fatal(err)
	}
	_, _, options, _, err := ssh.ParseAuthorizedKey(line)
	if err != nil {
		t.Fatal(err)
	}
	var decoded string
	for _, option := range options {
		if strings.HasPrefix(option, "command=\"") {
			decoded = strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(option, "command=\""), "\""), "\\\"", "\"")
		}
	}
	if decoded == "" {
		t.Fatal("temporary key has no fixed command")
	}
	out, err := exec.Command("/bin/sh", "-c", decoded).CombinedOutput()
	if err != nil {
		t.Fatalf("fixed command: %v: %s", err, out)
	}
	if string(out) != "registration-relay\n-port\n7717\n" {
		t.Fatalf("arguments: %q", out)
	}
}

func TestRegistrationAccountLinuxRejectsDifferentHome(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, _, err := registrationAccount(); err == nil {
		t.Fatal("accepted a different home directory")
	}
}
