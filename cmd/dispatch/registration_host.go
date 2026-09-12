package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/daemon"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
	"golang.org/x/crypto/ssh"
)

func hostRegistration(address string, port int) (*daemon.HostRegistration, string, error) {
	if runtime.GOOS != "windows" {
		return nil, "", errors.New("code registration requires a standard local Windows account")
	}
	// Membership is checked against the identity, including a filtered admin token.
	check := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", `$ErrorActionPreference='Stop'; $identity=[System.Security.Principal.WindowsIdentity]::GetCurrent(); if ($identity.Groups.Value -contains 'S-1-5-32-544') { throw 'Use a standard local Windows account to run the Daemon and SSH' }; $user=Get-LocalUser -SID $identity.User; if (-not $user.Enabled) { throw 'The local account is disabled' }; $user.Name`)
	b, err := check.Output()
	if err != nil {
		return nil, "", errors.New("registration requires the Daemon and SSH to use the same enabled standard local Windows account")
	}
	user := strings.TrimSpace(string(b))
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, "", err
	}
	dir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, "", err
	}
	k := &registrationKeys{path: filepath.Join(dir, "authorized_keys"), exe: exe, port: port}
	k.lock, err = lockRegistration(filepath.Join(dir, ".dispatch-registration.lock"))
	if err != nil {
		return nil, "", errors.New("another Daemon owns Host Registration for this account")
	}
	keepLock := false
	defer func() {
		if !keepLock {
			k.Close()
		}
	}()
	// Only this account, SYSTEM and Administrators may write the authorization
	// directory. Refuse broad grants rather than rewriting the user's ACL.
	acl := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", registrationACLCheck)
	if err := acl.Run(); err != nil {
		return nil, "", errors.New("other accounts can write the SSH authorization path; correct its permissions before registration")
	}
	pub, err := os.ReadFile(filepath.Join(os.Getenv("ProgramData"), "ssh", "ssh_host_ed25519_key.pub"))
	if err != nil {
		return nil, "", fmt.Errorf("read the OpenSSH ed25519 Host public key: %w", err)
	}
	key, _, _, _, err := ssh.ParseAuthorizedKey(pub)
	if err != nil || key.Type() != ssh.KeyAlgoED25519 {
		return nil, "", errors.New("OpenSSH must have an ed25519 Host key")
	}
	if err := k.sweep(); err != nil {
		return nil, "", err
	}
	s := daemon.NewHostRegistration(k)
	code, err := s.Begin(withPort(address), user, ssh.FingerprintSHA256(key), port)
	if err == nil {
		c, parseErr := protocol.ParseRegistrationCode(code)
		if parseErr != nil {
			err = parseErr
		} else {
			err = checkTemporarySSH(c, key)
		}
		if err != nil {
			s.Cancel()
			return nil, "", fmt.Errorf("OpenSSH did not pass the temporary-key restrictions check: %w", err)
		}
	}
	keepLock = err == nil
	return s, code, err
}

func checkTemporarySSH(c protocol.RegistrationCode, hostKey ssh.PublicKey) error {
	_, port, err := net.SplitHostPort(c.Address)
	if err != nil {
		return err
	}
	signer, err := ssh.NewSignerFromKey(ed25519.NewKeyFromSeed(c.Seed))
	if err != nil {
		return err
	}
	address := net.JoinHostPort("127.0.0.1", port)
	conn, err := net.DialTimeout("tcp", address, 10*time.Second)
	if err != nil {
		return errors.New("OpenSSH must accept SSH on loopback at the selected port")
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(20 * time.Second))
	sshConn, channels, requests, err := ssh.NewClientConn(conn, address, &ssh.ClientConfig{User: c.User, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.FixedHostKey(hostKey), HostKeyAlgorithms: []string{ssh.KeyAlgoED25519}})
	if err != nil {
		return errors.New("temporary key login failed; check the standard account, OpenSSH configuration and permissions")
	}
	client := ssh.NewClient(sshConn, channels, requests)
	defer client.Close()
	forward, err := client.Dial("tcp", "127.0.0.1:"+strconv.Itoa(c.DaemonPort))
	if err == nil {
		forward.Close()
		return errors.New("OpenSSH allowed temporary-key forwarding")
	}
	var denied *ssh.OpenChannelError
	if !errors.As(err, &denied) || denied.Reason != ssh.Prohibited {
		return errors.New("OpenSSH did not explicitly prohibit temporary-key forwarding")
	}
	listener, err := client.Listen("tcp", "127.0.0.1:0")
	if err == nil {
		listener.Close()
		return errors.New("OpenSSH allowed remote forwarding")
	}
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	if session.RequestPty("vt100", 24, 80, ssh.TerminalModes{}) == nil {
		return errors.New("OpenSSH allowed a temporary-key PTY")
	}
	if ok, err := session.SendRequest("auth-agent-req@openssh.com", true, nil); err != nil || ok {
		return errors.New("OpenSSH did not refuse agent forwarding")
	}
	output, err := session.CombinedOutput("cmd.exe /c echo dispatch-unrestricted-probe")
	var exit *ssh.ExitError
	if !errors.As(err, &exit) || exit.ExitStatus() != 1 || len(output) != 0 {
		return errors.New("the temporary key did not enforce the fixed registration command")
	}
	return nil
}

// The forced SSH command forwards one bounded, signed operation. It accepts no
// path, URL, account, or command from the SSH client.
func registrationRelay(ctx context.Context, args []string, out io.Writer) int {
	f := flag.NewFlagSet("registration-relay", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	port := f.Int("port", 0, "Daemon port")
	if f.Parse(args) != nil || *port < 1 || *port > 65535 || os.Getenv("SSH_ORIGINAL_COMMAND") != "dispatch-register" {
		return 1
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	stopRead := context.AfterFunc(ctx, func() { os.Stdin.Close() })
	defer stopRead()
	b, err := io.ReadAll(io.LimitReader(os.Stdin, protocol.RegistrationLimit+1))
	if err != nil || len(b) > protocol.RegistrationLimit {
		return 1
	}
	r, err := http.NewRequestWithContext(ctx, "POST", "http://127.0.0.1:"+strconv.Itoa(*port)+"/registration", bytes.NewReader(b))
	if err != nil {
		return 1
	}
	r.Header.Set("Content-Type", "application/json")
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(r)
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return 1
	}
	fmt.Fprintln(out, "registered")
	return 0
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
