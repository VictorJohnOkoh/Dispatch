package main

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

func registrationAccount() (string, string, error) {
	// Membership is checked against the identity, including a filtered admin token.
	check := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", `$ErrorActionPreference='Stop'; $identity=[System.Security.Principal.WindowsIdentity]::GetCurrent(); if ($identity.Groups.Value -contains 'S-1-5-32-544') { throw 'Use a standard local Windows account to run the Daemon and SSH' }; $user=Get-LocalUser -SID $identity.User; if (-not $user.Enabled) { throw 'The local account is disabled' }; $user.Name`)
	b, err := check.Output()
	if err != nil {
		return "", "", errors.New("registration requires the Daemon and SSH to use the same enabled standard local Windows account")
	}
	user := strings.TrimSpace(string(b))
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}

	return user, home, nil
}

func checkRegistrationPermissions(home string) error {
	acl := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", registrationACLCheck)
	if err := acl.Run(); err != nil {
		return errors.New("other accounts can write the SSH authorization path; correct its permissions before registration")
	}
	return nil
}

func registrationHostKeyPath() string {
	return filepath.Join(os.Getenv("ProgramData"), "ssh", "ssh_host_ed25519_key.pub")
}

func registrationCommand(exe string, port int) string {
	script := "& '" + strings.ReplaceAll(exe, "'", "''") + "' registration-relay -port " + fmt.Sprint(port)
	units := utf16.Encode([]rune(script))
	b := make([]byte, len(units)*2)
	for i, u := range units {
		binary.LittleEndian.PutUint16(b[i*2:], u)
	}
	command := "powershell.exe -NoProfile -NonInteractive -EncodedCommand " + base64.StdEncoding.EncodeToString(b)

	return command
}

const registrationACLCheck = `
$ErrorActionPreference = 'Stop'
$sid = [System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value
$trusted = @($sid, 'S-1-5-18', 'S-1-5-32-544')
$change = [System.Security.AccessControl.FileSystemRights]'Write, Delete, DeleteSubdirectoriesAndFiles, ChangePermissions, TakeOwnership'
$paths = @((Join-Path $env:USERPROFILE '.ssh'))
$file = Join-Path $paths[0] 'authorized_keys'
if (Test-Path -LiteralPath $file) { $paths += $file }
foreach ($p in $paths) {
  if ((Get-Item -Force -LiteralPath $p).Attributes -band [System.IO.FileAttributes]::ReparsePoint) {
    throw 'Registration requires ordinary SSH authorization paths, not links'
  }
  $acl = Get-Acl -LiteralPath $p
  if ($acl.GetOwner([System.Security.Principal.SecurityIdentifier]).Value -notin $trusted) {
    throw 'Another account owns the SSH authorization path'
  }
  foreach ($rule in $acl.Access) {
    if ($rule.AccessControlType -eq 'Allow' -and ($rule.FileSystemRights -band $change) -ne 0) {
      $who = $rule.IdentityReference.Translate([System.Security.Principal.SecurityIdentifier]).Value
      if ($who -notin $trusted) { throw 'Other accounts can change the SSH authorization path' }
    }
  }
}
`

// OpenSSH can leave the public Host key readable only to administrators, and the
// Daemon runs as a standard account. The key is public, so read is all it grants.
func registrationHostKeyAdvice(path, user string) string {
	return fmt.Sprintf("this account cannot read it; in an administrator PowerShell run: icacls \"%s\" /grant \"%s:(R)\"", path, user)
}
