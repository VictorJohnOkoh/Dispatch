package main

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func registrationAccount() (string, string, error) {
	if os.Geteuid() == 0 || os.Getuid() != os.Geteuid() {
		return "", "", errors.New("run Host Registration as the non-root account used for SSH, without sudo")
	}
	account, err := user.LookupId(strconv.Itoa(os.Geteuid()))
	if err != nil {
		return "", "", fmt.Errorf("find the Daemon account: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil || home != account.HomeDir || !filepath.IsAbs(home) {
		return "", "", errors.New("HOME must match the SSH account's home directory")
	}
	return account.Username, home, nil
}

func checkRegistrationPermissions(home string) error {
	dir := filepath.Join(home, ".ssh")
	file := filepath.Join(dir, "authorized_keys")
	for _, path := range []string{home, dir, file} {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) && path == file {
			continue
		}
		if err != nil {
			return err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0o022 != 0 {
			return fmt.Errorf("%s must be owned by the Daemon account and must not allow group or other write access", path)
		}
		if (path == file && !info.Mode().IsRegular()) || (path != file && !info.IsDir()) {
			return fmt.Errorf("%s must be an ordinary SSH authorization path, not a link", path)
		}
	}
	return nil
}

func registrationHostKeyPath() string {
	return "/etc/ssh/ssh_host_ed25519_key.pub"
}

func registrationCommand(exe string, port int) string {
	return "exec '" + strings.ReplaceAll(exe, "'", "'\"'\"'") + "' registration-relay -port " + strconv.Itoa(port)
}

// The key is public, so read for everyone is what it should already have.
func registrationHostKeyAdvice(path, _ string) string {
	return fmt.Sprintf("this account cannot read it; run: sudo chmod 644 %s", path)
}
