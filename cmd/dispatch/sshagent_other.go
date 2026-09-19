//go:build !windows

package main

import (
	"errors"
	"io"
	"net"
	"os"
)

func sshAgentPipe() (io.ReadWriteCloser, error) {
	socket := os.Getenv("SSH_AUTH_SOCK")
	if socket == "" {
		return nil, errors.New("SSH_AUTH_SOCK names no agent")
	}
	return net.Dial("unix", socket)
}
