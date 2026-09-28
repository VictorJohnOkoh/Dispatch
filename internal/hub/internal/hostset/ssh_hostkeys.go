package hostset

import (
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"slices"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func hostKeyAlgorithms(key ssh.PublicKey) []string {
	switch key.Type() {
	case ssh.KeyAlgoRSA:
		return []string{ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSASHA512}
	case ssh.CertAlgoRSAv01:
		return []string{ssh.CertAlgoRSASHA256v01, ssh.CertAlgoRSASHA512v01}
	default:
		return []string{key.Type()}
	}
}

// A Host can offer several keys. Negotiate one the trust files name instead of
// selecting an untrusted key type before the callback can check its value.
func knownHostKeyAlgorithms(files []string, address string) ([]string, error) {
	var algorithms []string
	for _, file := range files {
		data, err := os.ReadFile(file)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		check, err := knownhosts.New(file)
		if err != nil {
			return nil, err
		}
		for len(data) > 0 {
			marker, _, key, _, rest, err := ssh.ParseKnownHosts(data)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, err
			}
			data = rest
			// The callback matches address, including hashed names and patterns.
			if marker != "" || check(address, &net.TCPAddr{}, key) != nil {
				continue
			}
			for _, algorithm := range hostKeyAlgorithms(key) {
				if !slices.Contains(algorithms, algorithm) {
					algorithms = append(algorithms, algorithm)
				}
			}
		}
	}
	return algorithms, nil
}
