package main

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
)

// registrationAccount names the account this Daemon runs as. root is the Linux
// administrator.
func registrationAccount() (string, bool, error) {
	account, err := user.LookupId(strconv.Itoa(os.Geteuid()))
	if err != nil {
		return "", false, fmt.Errorf("find the Daemon account: %w", err)
	}
	return account.Username, os.Geteuid() == 0, nil
}

func registrationHostKeyPath() string {
	return "/etc/ssh/ssh_host_ed25519_key.pub"
}

// The key is public, so read for everyone is what it should already have.
func registrationHostKeyAdvice(path, _ string) string {
	return fmt.Sprintf("this account cannot read it; run: sudo chmod 644 %s", path)
}
