package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegistrationACLRefusesDeleteAndPermissionChanges(t *testing.T) {
	for _, right := range []string{"D", "WDAC", "WO", "DC"} {
		t.Run(right, func(t *testing.T) {
			home := t.TempDir()
			dir := filepath.Join(home, ".ssh")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			account, err := user.Current()
			if err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command("icacls.exe", dir, "/inheritance:r", "/grant:r", "*"+account.Uid+":(OI)(CI)(F)").CombinedOutput(); err != nil {
				t.Fatalf("private fixture ACL: %v: %s", err, out)
			}
			check := func() error {
				cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", registrationACLCheck)
				cmd.Env = append(os.Environ(), "USERPROFILE="+home)
				out, err := cmd.CombinedOutput()
				if err != nil {
					return fmt.Errorf("%w: %s", err, out)
				}
				return nil
			}
			if err := check(); err != nil {
				t.Fatal("private fixture was refused", err)
			}
			if out, err := exec.Command("icacls.exe", dir, "/grant", "*S-1-1-0:("+right+")").CombinedOutput(); err != nil {
				t.Fatalf("fixture ACL: %v: %s", err, out)
			}
			if check() == nil {
				t.Fatal("accepted an authorization directory another account can change")
			}
		})
	}
}

// The advice is the whole value of this error, so it names the file to change
// and the account to grant, and nothing a user has to work out.
func TestTheHostKeyAdviceNamesTheFileAndTheAccount(t *testing.T) {
	advice := registrationHostKeyAdvice(`C:\ProgramData\ssh\ssh_host_ed25519_key.pub`, "victor")
	for _, want := range []string{"icacls", `C:\ProgramData\ssh\ssh_host_ed25519_key.pub`, "victor:(R)"} {
		if !strings.Contains(advice, want) {
			t.Errorf("advice = %q, want it to contain %q", advice, want)
		}
	}
}
