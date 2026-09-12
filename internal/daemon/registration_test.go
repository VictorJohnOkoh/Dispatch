package daemon

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

type memoryRegistrationKeys struct {
	key       []byte
	completed bool
	fail      bool
}

func (k *memoryRegistrationKeys) Temporary(protocol.RegistrationCode) error { return nil }
func (k *memoryRegistrationKeys) Claim(_ string, pub []byte, _ time.Time) error {
	if k.fail {
		return errors.New("write failed")
	}
	k.key = append([]byte(nil), pub...)
	return nil
}
func (k *memoryRegistrationKeys) Complete(_ string, pub []byte) error {
	if !equalKey(k.key, pub) {
		return errors.New("wrong key")
	}
	k.completed = true
	return nil
}
func (k *memoryRegistrationKeys) Remove(string) error {
	if !k.completed {
		k.key = nil
	}
	return nil
}
func (k *memoryRegistrationKeys) Completed(_ string, pub []byte) bool {
	return k.completed && equalKey(k.key, pub)
}

func registrationForTest(t *testing.T) (*HostRegistration, *memoryRegistrationKeys, protocol.RegistrationCode) {
	t.Helper()
	keys := &memoryRegistrationKeys{}
	s := NewHostRegistration(keys)
	raw, err := s.Begin("127.0.0.1:22", "localuser", "SHA256:"+base64.RawStdEncoding.EncodeToString(make([]byte, 32)), 7717)
	if err != nil {
		t.Fatal(err)
	}
	c, err := protocol.ParseRegistrationCode(raw)
	if err != nil {
		t.Fatal(err)
	}
	return s, keys, c
}
func requestForRegistration(c protocol.RegistrationCode, action string, pub ed25519.PublicKey, key ed25519.PrivateKey) protocol.RegistrationRequest {
	r := protocol.RegistrationRequest{ID: c.ID, Action: action, PublicKey: pub}
	r.Signature = ed25519.Sign(key, r.SigningBytes())
	return r
}

func TestRegistrationClaimsAreExclusiveAndRetryable(t *testing.T) {
	s, keys, c := registrationForTest(t)
	temp := ed25519.NewKeyFromSeed(c.Seed)
	a, _, _ := ed25519.GenerateKey(rand.Reader)
	b, _, _ := ed25519.GenerateKey(rand.Reader)
	claims := []protocol.RegistrationRequest{requestForRegistration(c, "claim", a, temp), requestForRegistration(c, "claim", b, temp)}
	var wg sync.WaitGroup
	results := make(chan int, 2)
	for i, r := range claims {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s.Apply(r) == nil {
				results <- i
			}
		}()
	}
	wg.Wait()
	close(results)
	winner := -1
	count := 0
	for i := range results {
		winner = i
		count++
	}
	if count != 1 {
		t.Fatalf("%d claims succeeded", count)
	}
	if err := s.Apply(claims[winner]); err != nil {
		t.Fatal(err)
	}
	if !equalKey(keys.key, claims[winner].PublicKey) {
		t.Fatal("retry changed authorization")
	}
}

func TestRegistrationCompletionSurvivesRestartAndLateAbort(t *testing.T) {
	s, keys, c := registrationForTest(t)
	temp := ed25519.NewKeyFromSeed(c.Seed)
	pub, private, _ := ed25519.GenerateKey(rand.Reader)
	claim := requestForRegistration(c, "claim", pub, temp)
	if err := s.Apply(claim); err != nil {
		t.Fatal(err)
	}
	complete := requestForRegistration(c, "complete", pub, private)
	if err := s.Apply(complete); err != nil {
		t.Fatal(err)
	}
	s = NewHostRegistration(keys)
	if err := s.Apply(complete); err != nil {
		t.Fatal("lost completion reply was not recoverable", err)
	}
	if s.Apply(requestForRegistration(c, "abort", pub, temp)) == nil {
		t.Fatal("temporary key remained usable")
	}
	if err := s.Cancel(); err != nil {
		t.Fatal(err)
	}
	if !keys.completed || !equalKey(keys.key, pub) {
		t.Fatal("committed access was revoked")
	}
}

func TestRegistrationExpiryRestartAndSignatureRefuseAccess(t *testing.T) {
	for _, mode := range []string{"expiry", "restart", "cancel", "signature"} {
		t.Run(mode, func(t *testing.T) {
			s, keys, c := registrationForTest(t)
			pub, _, _ := ed25519.GenerateKey(rand.Reader)
			r := requestForRegistration(c, "claim", pub, ed25519.NewKeyFromSeed(c.Seed))
			switch mode {
			case "expiry":
				s.now = func() time.Time { return time.Unix(c.Expires, 0) }
			case "restart":
				s = NewHostRegistration(keys)
			case "cancel":
				s.Cancel()
			case "signature":
				r.Signature[0] ^= 1
			}
			if s.Apply(r) == nil || keys.key != nil {
				t.Fatal("invalid registration gained access")
			}
		})
	}
}

func TestFailedClaimWriteCanBeRetriedOnlyByItsKey(t *testing.T) {
	s, keys, c := registrationForTest(t)
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	temp := ed25519.NewKeyFromSeed(c.Seed)
	r := requestForRegistration(c, "claim", pub, temp)
	keys.fail = true
	if s.Apply(r) == nil {
		t.Fatal("write should fail")
	}
	keys.fail = false
	if s.Apply(requestForRegistration(c, "claim", other, temp)) == nil {
		t.Fatal("competing key took failed claim")
	}
	if err := s.Apply(r); err != nil {
		t.Fatal(err)
	}
}
