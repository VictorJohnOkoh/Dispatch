package main

import (
	"strings"
	"testing"
)

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
