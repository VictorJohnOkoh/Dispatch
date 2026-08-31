package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/vendors"
)

func writeFile(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const goodDaemon = `{
  "listen": "127.0.0.1:7717",
  "workspaceRoot": "/srv/work",
  "logPath": "/var/lib/dispatch/events.db",
  "vendors": [{"kind": "ollama", "base": "http://127.0.0.1:11434"}],
  "harnesses": [
    {"name": "passthrough"},
    {"name": "opencode", "exe": "/usr/local/bin/opencode"}
  ],
  "policyDefault": {"read": "auto", "edit": "wait", "execute": "wait", "fetch": "auto", "other": "wait"}
}`

const goodHub = `{
  "listen": "127.0.0.1:7700",
  "hosts": [{"id": "workstation", "address": "10.0.0.4:22", "user": "victor", "keyPath": "/home/victor/.ssh/id_ed25519", "daemonPort": 7717}]
}`

// noPolicyDaemon is the good file with the policyDefault key left out, which is
// the one way five empty slots reach Validate.
func noPolicyDaemon() string {
	head, _, _ := strings.Cut(goodDaemon, `,
  "policyDefault"`)
	return head + "\n}"
}

func TestLoadDaemonCarriesEveryField(t *testing.T) {
	got, err := LoadDaemon(writeFile(t, "daemon.json", goodDaemon))
	if err != nil {
		t.Fatalf("LoadDaemon: %v", err)
	}
	if got.Listen != "127.0.0.1:7717" || got.WorkspaceRoot != "/srv/work" || got.LogPath != "/var/lib/dispatch/events.db" {
		t.Errorf("got %+v", got)
	}
	want := vendors.Endpoint{Kind: vendors.Ollama, Base: "http://127.0.0.1:11434"}
	if len(got.Vendors) != 1 || got.Vendors[0].Endpoint != want {
		t.Errorf("Vendors = %+v, want one %+v", got.Vendors, want)
	}
	if len(got.Harnesses) != 2 || got.Harnesses[1].Exe != "/usr/local/bin/opencode" {
		t.Errorf("Harnesses = %+v", got.Harnesses)
	}
	if got.PolicyDefault[event.ToolExecute] != event.RuleWait {
		t.Errorf("PolicyDefault execute = %q, want wait", got.PolicyDefault[event.ToolExecute])
	}
}

func TestLoadHubCarriesEveryHost(t *testing.T) {
	got, err := LoadHub(writeFile(t, "hub.json", goodHub))
	if err != nil {
		t.Fatalf("LoadHub: %v", err)
	}
	if len(got.Hosts) != 1 {
		t.Fatalf("Hosts = %+v, want one", got.Hosts)
	}
	h := got.Hosts[0]
	if h.ID != "workstation" || h.Address != "10.0.0.4:22" || h.User != "victor" || h.DaemonPort != 7717 {
		t.Errorf("host = %+v", h)
	}
}

// The fence: a Daemon has no field naming another Host, so a peers key is a
// startup error and not a key that is quietly dropped.
func TestPeersKeyFailsAndNamesItself(t *testing.T) {
	body := strings.Replace(goodDaemon, `"listen"`, `"peers": ["other-host"],
  "listen"`, 1)
	_, err := LoadDaemon(writeFile(t, "daemon.json", body))
	if err == nil {
		t.Fatal("LoadDaemon accepted a peers key")
	}
	if !strings.Contains(err.Error(), "peers") {
		t.Errorf("error does not name the key: %v", err)
	}
}

func TestUnknownKeyFailsAndNamesItself(t *testing.T) {
	cases := []struct {
		name string
		body string
		key  string
		load func(string) error
	}{
		{"daemon top level", strings.Replace(goodDaemon, `"listen"`, `"lisen"`, 1), "lisen",
			func(p string) error { _, err := LoadDaemon(p); return err }},
		{"vendor profile", strings.Replace(goodDaemon, `"kind": "ollama"`, `"kind": "ollama", "prot": 11434`, 1), "prot",
			func(p string) error { _, err := LoadDaemon(p); return err }},
		{"harness profile", strings.Replace(goodDaemon, `"name": "passthrough"`, `"name": "passthrough", "args": []`, 1), "args",
			func(p string) error { _, err := LoadDaemon(p); return err }},
		{"policy slot", strings.Replace(goodDaemon, `"read": "auto"`, `"read": "auto", "network": "auto"`, 1), "network",
			func(p string) error { _, err := LoadDaemon(p); return err }},
		{"hub top level", strings.Replace(goodHub, `"hosts"`, `"host"`, 1), "host",
			func(p string) error { _, err := LoadHub(p); return err }},
		{"host profile", strings.Replace(goodHub, `"id": "workstation"`, `"id": "workstation", "port": 22`, 1), "port",
			func(p string) error { _, err := LoadHub(p); return err }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.load(writeFile(t, "config.json", c.body))
			if err == nil {
				t.Fatalf("load accepted the unknown key %q", c.key)
			}
			if !strings.Contains(err.Error(), c.key) {
				t.Errorf("error does not name %q: %v", c.key, err)
			}
		})
	}
}

func TestLoadRefusesBadValues(t *testing.T) {
	cases := []struct {
		name string
		body string
		load func(string) error
	}{
		{"no vendor", strings.Replace(goodDaemon, `[{"kind": "ollama", "base": "http://127.0.0.1:11434"}]`, `[]`, 1),
			func(p string) error { _, err := LoadDaemon(p); return err }},
		{"unknown vendor kind", strings.Replace(goodDaemon, `"ollama"`, `"vllm"`, 1),
			func(p string) error { _, err := LoadDaemon(p); return err }},
		{"bare harness exe", strings.Replace(goodDaemon, `/usr/local/bin/opencode`, `opencode`, 1),
			func(p string) error { _, err := LoadDaemon(p); return err }},
		{"harness named twice", strings.Replace(goodDaemon, `"name": "opencode"`, `"name": "passthrough"`, 1),
			func(p string) error { _, err := LoadDaemon(p); return err }},
		{"no policy at all", noPolicyDaemon(),
			func(p string) error { _, err := LoadDaemon(p); return err }},
		{"policy slot missing", strings.Replace(goodDaemon, `"fetch": "auto", `, ``, 1),
			func(p string) error { _, err := LoadDaemon(p); return err }},
		{"no workspace root", strings.Replace(goodDaemon, `"workspaceRoot": "/srv/work"`, `"workspaceRoot": ""`, 1),
			func(p string) error { _, err := LoadDaemon(p); return err }},
		{"host named twice", strings.Replace(goodHub, `"hosts": [`, `"hosts": [{"id": "workstation", "address": "10.0.0.5:22", "user": "v", "keyPath": "/k", "daemonPort": 7717}, `, 1),
			func(p string) error { _, err := LoadHub(p); return err }},
		{"no host", strings.Replace(goodHub, `"hosts": [`, `"hosts0": [`, 1),
			func(p string) error { _, err := LoadHub(p); return err }},
		{"daemon port out of range", strings.Replace(goodHub, `"daemonPort": 7717`, `"daemonPort": 0`, 1),
			func(p string) error { _, err := LoadHub(p); return err }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.load(writeFile(t, "config.json", c.body)); err == nil {
				t.Fatal("load accepted it")
			}
		})
	}
}

func TestLoadRefusesASecondValue(t *testing.T) {
	if _, err := LoadDaemon(writeFile(t, "daemon.json", goodDaemon+goodDaemon)); err == nil {
		t.Fatal("LoadDaemon read the first of two values")
	}
}

func TestLoadNamesTheMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.json")
	_, err := LoadDaemon(path)
	if err == nil || !strings.Contains(err.Error(), "absent.json") {
		t.Fatalf("error does not name the file: %v", err)
	}
}

// The example files ship for a human to copy, so a change that stops them
// loading is a change that ships a broken example.
func TestExampleFilesLoad(t *testing.T) {
	if _, err := LoadDaemon(filepath.Join("..", "..", "daemon.example.json")); err != nil {
		t.Errorf("daemon.example.json: %v", err)
	}
	if _, err := LoadHub(filepath.Join("..", "..", "hub.example.json")); err != nil {
		t.Errorf("hub.example.json: %v", err)
	}
}
