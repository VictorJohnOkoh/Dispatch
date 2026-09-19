//go:build !windows && !linux

package main

import "errors"

func registrationAccount() (string, string, error) {
	return "", "", errors.New("Host Registration requires Windows or Linux")
}

func checkRegistrationPermissions(string) error {
	return errors.New("Host Registration requires Windows or Linux")
}

func registrationHostKeyPath() string { return "" }

func registrationCommand(string, int) string { return "" }

func registrationHostKeyAdvice(string, string) string { return "" }
