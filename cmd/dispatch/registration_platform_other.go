//go:build !windows && !linux

package main

import "errors"

func registrationAccount() (string, bool, error) {
	return "", false, errors.New("Host Registration requires Windows or Linux")
}

func registrationHostKeyPath() string { return "" }

func registrationHostKeyAdvice(string, string) string { return "" }
