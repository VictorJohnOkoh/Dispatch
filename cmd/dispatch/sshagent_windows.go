package main

import (
	"io"
	"os"
)

// Windows OpenSSH serves its agent on a named pipe rather than a socket, and a
// pipe opens as a file. Only the reads and writes are needed, so a file is
// enough and no socket type is involved.
const windowsAgentPipe = `\.\pipe\openssh-ssh-agent`

func sshAgentPipe() (io.ReadWriteCloser, error) {
	return os.OpenFile(windowsAgentPipe, os.O_RDWR, 0)
}
