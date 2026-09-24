package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// registrationAccount names the local account this Daemon runs as, and whether it
// has administrator rights. The token's Groups leave out a deny-only group, so a
// Daemon started without elevation is not an administrator here, and neither are
// the Harness processes it starts.
func registrationAccount() (string, bool, error) {
	check := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", `$ErrorActionPreference='Stop'; $identity=[System.Security.Principal.WindowsIdentity]::GetCurrent(); $user=Get-LocalUser -SID $identity.User; if (-not $user.Enabled) { throw 'disabled' }; $user.Name; $identity.Groups.Value -contains 'S-1-5-32-544'`)
	b, err := check.Output()
	// One line each for the name and the membership. A local name may hold spaces.
	name, member, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	name, member = strings.TrimSpace(name), strings.TrimSpace(member)
	if err != nil || name == "" || (member != "True" && member != "False") {
		return "", false, errors.New("Host Registration needs the Daemon to run as an enabled local Windows account")
	}
	return name, member == "True", nil
}

func registrationHostKeyPath() string {
	return filepath.Join(os.Getenv("ProgramData"), "ssh", "ssh_host_ed25519_key.pub")
}

// OpenSSH can leave the public Host key readable only to administrators. The key
// is public, so read is all this grants.
func registrationHostKeyAdvice(path, user string) string {
	return fmt.Sprintf("this account cannot read it; in an administrator PowerShell run: icacls \"%s\" /grant \"%s:(R)\"", path, user)
}
