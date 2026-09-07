package hostset_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/VictorJohnOkoh/Dispatch/internal/hub/internal/hostset"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func TestRegisteringAHostRunsTheChecksInOrder(t *testing.T) {
	world := registrable(t)
	daemon := daemonListener(t, http.StatusOK)
	dir := t.TempDir()

	user := &person{fingerprint: make(chan string, 1), password: "letmein"}
	var got hostset.Registered
	err := hostset.RegisterHost(t.Context(), request(world, dir, daemonPort(t, daemon)), user,
		func(host hostset.Registered) error { got = host; return nil })
	if err != nil {
		t.Fatalf("RegisterHost = %v", err)
	}

	// The fingerprint the user was shown is this Host's own, and it was shown
	// before anything secret went to the Host.
	if want := ssh.FingerprintSHA256(world.hostKey); <-user.fingerprint != want {
		t.Errorf("the user was shown a fingerprint that is not this Host's")
	}
	want := []string{"password login", "forward", "authorize", "key login", "forward"}
	if steps := world.done(); !slices.Equal(steps, want) {
		t.Errorf("the Host was asked to do %v, want %v", steps, want)
	}

	if len(world.keys()) != 2 || !strings.Contains(world.keys()[1], "dispatch-hub") {
		t.Errorf("authorized_keys = %v, want the Hub's key added once", world.keys())
	}
	key := filepath.Join(dir, "id_ed25519")
	if got != (hostset.Registered{
		ID: "desk", Address: world.address, User: "victor",
		KeyPath: key, KnownHosts: filepath.Join(dir, "known_hosts"), DaemonPort: daemonPort(t, daemon),
	}) {
		t.Errorf("committed %+v", got)
	}
	if _, err := os.Stat(key); err != nil {
		t.Errorf("the managed identity: %v", err)
	}
	trusted := string(readFile(t, filepath.Join(dir, "known_hosts")))
	if !strings.Contains(trusted, knownhosts.Line([]string{world.address}, world.hostKey)) {
		t.Errorf("known_hosts = %q, want this Host's key in it", trusted)
	}
}

// The Hub has one identity, so a second Host uses the key the first one made and
// the account it is already authorized on gains nothing.
func TestASecondRegistrationReusesTheManagedKey(t *testing.T) {
	world := registrable(t)
	daemon := daemonListener(t, http.StatusOK)
	dir := t.TempDir()
	req := request(world, dir, daemonPort(t, daemon))
	commit := func(hostset.Registered) error { return nil }

	if err := hostset.RegisterHost(t.Context(), req, newPerson("letmein"), commit); err != nil {
		t.Fatalf("first RegisterHost = %v", err)
	}
	first := readFile(t, filepath.Join(dir, "id_ed25519"))

	if err := hostset.RegisterHost(t.Context(), req, newPerson("letmein"), commit); err != nil {
		t.Fatalf("second RegisterHost = %v", err)
	}
	if string(readFile(t, filepath.Join(dir, "id_ed25519"))) != string(first) {
		t.Error("the second registration made a new managed key")
	}
	if len(world.keys()) != 2 {
		t.Errorf("authorized_keys = %v, want the Hub's key in it once", world.keys())
	}
	if lines := strings.Count(string(readFile(t, filepath.Join(dir, "known_hosts"))), "\n"); lines != 1 {
		t.Errorf("known_hosts has %d lines, want 1", lines)
	}
}

// The fingerprint is the user's one chance to see which machine this is, so a
// declined one ends the attempt with the password never read.
func TestADeclinedFingerprintSendsNoPassword(t *testing.T) {
	world := registrable(t)
	daemon := daemonListener(t, http.StatusOK)

	user := newPerson("letmein")
	user.declines = true
	err := hostset.RegisterHost(t.Context(), request(world, t.TempDir(), daemonPort(t, daemon)), user,
		func(hostset.Registered) error { t.Error("a declined Host was committed"); return nil })
	if !errors.Is(err, hostset.ErrDeclined) {
		t.Fatalf("RegisterHost = %v, want ErrDeclined", err)
	}
	if user.asked {
		t.Error("the password was read after the fingerprint was declined")
	}
	if steps := world.done(); len(steps) != 0 {
		t.Errorf("the Host was asked to do %v, want nothing", steps)
	}
}

// A Daemon that serves another protocol version is a Host to update, and it is
// found before the Host is changed at all.
func TestAnIncompatibleDaemonChangesNothing(t *testing.T) {
	world := registrable(t)
	daemon := daemonListener(t, protocol.StatusUpgradeRequired)
	dir := t.TempDir()

	err := hostset.RegisterHost(t.Context(), request(world, dir, daemonPort(t, daemon)), newPerson("letmein"),
		func(hostset.Registered) error { t.Error("an Incompatible Host was committed"); return nil })
	if !errors.Is(err, hostset.ErrIncompatible) {
		t.Fatalf("RegisterHost = %v, want ErrIncompatible", err)
	}
	if !strings.Contains(err.Error(), "[2]") {
		t.Errorf("RegisterHost = %v, want the versions it serves", err)
	}
	if len(world.keys()) != 1 {
		t.Errorf("authorized_keys = %v, want the Hub's key never added", world.keys())
	}
	if _, err := os.Stat(filepath.Join(dir, "known_hosts")); err == nil {
		t.Error("a Host that failed the Handshake was trusted")
	}
}

// Nothing behind the tunnel is a Daemon that is not running, and the password
// connection finds it before the Host is changed.
func TestNoDaemonBehindTheTunnelStopsRegistration(t *testing.T) {
	world := registrable(t)
	daemon := daemonListener(t, http.StatusOK)
	port := daemonPort(t, daemon)
	daemon.Close()

	err := hostset.RegisterHost(t.Context(), request(world, t.TempDir(), port), newPerson("letmein"),
		func(hostset.Registered) error { t.Error("a Host with no Daemon was committed"); return nil })
	if !errors.Is(err, hostset.ErrNoDaemon) {
		t.Fatalf("RegisterHost = %v, want ErrNoDaemon", err)
	}
	if len(world.keys()) != 1 {
		t.Errorf("authorized_keys = %v, want the Hub's key never added", world.keys())
	}
}

// A failure after the Host has been changed takes the change back, so a second
// attempt starts where the first one did.
func TestAFailedCommitTakesTheAuthorizationOffAgain(t *testing.T) {
	world := registrable(t)
	daemon := daemonListener(t, http.StatusOK)
	dir := t.TempDir()

	refused := errors.New("this Host id is already in hub.json")
	err := hostset.RegisterHost(t.Context(), request(world, dir, daemonPort(t, daemon)), newPerson("letmein"),
		func(hostset.Registered) error { return refused })
	if !errors.Is(err, refused) {
		t.Fatalf("RegisterHost = %v, want the commit's own error", err)
	}
	if !strings.Contains(err.Error(), "taken off") {
		t.Errorf("RegisterHost = %v, want it to say the rollback ran", err)
	}
	if len(world.keys()) != 1 {
		t.Errorf("authorized_keys = %v, want the Hub's key taken off again", world.keys())
	}
	want := []string{"password login", "forward", "authorize", "key login", "forward", "deauthorize"}
	if steps := world.done(); !slices.Equal(steps, want) {
		t.Errorf("the Host was asked to do %v, want %v", steps, want)
	}

	// The Hub's own identity stays, because it is what the next attempt uses.
	if _, err := os.Stat(filepath.Join(dir, "id_ed25519")); err != nil {
		t.Errorf("the managed identity was removed: %v", err)
	}
	trusted, _ := os.ReadFile(filepath.Join(dir, "known_hosts"))
	if strings.Contains(string(trusted), knownhosts.Line([]string{world.address}, world.hostKey)) {
		t.Error("a Host that was not committed stays trusted")
	}
}

// An interrupt is one of the ways registration fails, and the rollback must not
// end with the thing that caused it.
func TestACancelledRegistrationStillTakesTheAuthorizationOff(t *testing.T) {
	world := registrable(t)
	daemon := daemonListener(t, http.StatusOK)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	err := hostset.RegisterHost(ctx, request(world, t.TempDir(), daemonPort(t, daemon)), newPerson("letmein"),
		func(hostset.Registered) error { cancel(); return ctx.Err() })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RegisterHost = %v, want the cancellation", err)
	}
	if len(world.keys()) != 1 {
		t.Errorf("authorized_keys = %v, want the Hub's key taken off again", world.keys())
	}
}

// The script runs on the Host one round trip before its answer arrives, so an
// authorization this Hub could not read may have happened. It is rolled back
// like every stage after it.
func TestAnAuthorizationWithNoAnswerIsRolledBack(t *testing.T) {
	world := registrable(t)
	world.silent = true
	daemon := daemonListener(t, http.StatusOK)

	err := hostset.RegisterHost(t.Context(), request(world, t.TempDir(), daemonPort(t, daemon)), newPerson("letmein"),
		func(hostset.Registered) error { t.Error("a Host that was not authorized was committed"); return nil })
	if err == nil || !strings.Contains(err.Error(), "authorizing the Hub's key") {
		t.Fatalf("RegisterHost = %v, want the authorization named", err)
	}
	if !slices.Contains(world.done(), "deauthorize") {
		t.Errorf("the Host was asked to do %v, want the rollback in it", world.done())
	}
	if len(world.keys()) != 1 {
		t.Errorf("authorized_keys = %v, want the Hub's key taken off again", world.keys())
	}
}

func TestRegisterHostRefusesAnAddressWithNoPort(t *testing.T) {
	req := hostset.Registration{ID: "desk", Address: "192.168.1.20", User: "victor", DaemonPort: 7717, Dir: t.TempDir()}
	err := hostset.RegisterHost(t.Context(), req, newPerson("letmein"),
		func(hostset.Registered) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "needs a port") {
		t.Fatalf("RegisterHost = %v, want an error naming the port", err)
	}
}

// registrable is a Host that takes the password Host Registration will send.
func registrable(t *testing.T) *sshWorld {
	t.Helper()
	world := newSSHWorld(t)
	world.password = "letmein"
	return world
}

func request(world *sshWorld, dir string, daemonPort int) hostset.Registration {
	return hostset.Registration{
		ID: "desk", Address: world.address, User: "victor", DaemonPort: daemonPort, Dir: dir,
	}
}

// daemonListener stands in for the Daemon's loopback port. It answers the Event
// stream the way the Handshake needs, or refuses the version.
func daemonListener(t *testing.T, status int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(protocol.VersionHeader) != "1" || r.URL.Path != "/v1/events" {
			http.Error(w, "not the Handshake", http.StatusBadRequest)
			return
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			fmt.Fprint(w, `{"reason":"protocol","speaks":[2]}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: %s\ndata: {\"protocol\":%d}\n\n", protocol.FrameHello, protocol.Version)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	return server
}

// person is the user at the keyboard: one fingerprint to confirm and one
// password to type.
type person struct {
	fingerprint chan string
	password    string
	declines    bool

	// asked says the password was read, which a declined fingerprint must not do.
	asked bool
}

func newPerson(password string) *person {
	return &person{fingerprint: make(chan string, 1), password: password}
}

func (p *person) ConfirmHostKey(fingerprint string) (bool, error) {
	select {
	case p.fingerprint <- fingerprint:
	default:
	}
	return !p.declines, nil
}

func (p *person) Password() (string, error) {
	p.asked = true
	return p.password, nil
}

// daemonPort is the loopback port the tunnel must reach.
func daemonPort(t *testing.T, server *httptest.Server) int {
	t.Helper()
	return portOf(t, strings.TrimPrefix(server.URL, "http://"))
}
