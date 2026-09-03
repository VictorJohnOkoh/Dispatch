package harness

import (
	"bufio"
	"context"
	"io"
	"sync"
	"testing"
	"time"
)

// transport is the pipe pair a scripted replay drives an Adapter over, and it is
// the half of a script that is the same whichever wire the Adapter speaks. What
// each protocol keeps for itself is the walk over its own capture and the rule
// for matching one of its own frames.

type transport struct {
	t *testing.T

	pipes   Pipes
	toAgent *io.PipeWriter
	fromMe  *io.PipeWriter // the Adapter's own end of stdin, closed at cleanup
	agent   *bufio.Scanner

	done chan struct{}

	mu        sync.Mutex
	abandoned bool // the test is over, so an unplayed frame is not a failure
}

func newTransport(t *testing.T) *transport {
	t.Helper()
	fromAdapter, adapterWrites := io.Pipe()
	adapterReads, scriptWrites := io.Pipe()
	x := &transport{
		t:       t,
		pipes:   Pipes{In: adapterWrites, Out: adapterReads},
		toAgent: scriptWrites,
		fromMe:  adapterWrites,
		agent:   bufio.NewScanner(fromAdapter),
		done:    make(chan struct{}),
	}
	x.agent.Buffer(make([]byte, 0, 64<<10), 8<<20)
	return x
}

// play runs the walk over the capture in the background. It is stopped before the
// test ends, because a script that reported a mismatch afterwards would panic
// rather than fail.
func (x *transport) play(walk func()) {
	go func() {
		defer close(x.done)
		walk()
	}()
	x.t.Cleanup(func() {
		x.abandon()
		x.toAgent.Close()
		x.fromMe.Close()
		<-x.done
	})
}

// spawn is the Spawner a test hands the Adapter. No process starts.
func (x *transport) spawn() Spawner {
	return func(context.Context, Launch) (Pipes, error) { return x.pipes, nil }
}

// feed writes one line to the Adapter and says whether the walk may go on.
func (x *transport) feed(line []byte) bool {
	_, err := x.toAgent.Write(append(line, '\n'))
	return err == nil
}

// hangUp ends the Adapter's stdout, which is the Harness process going away.
func (x *transport) hangUp() { x.toAgent.Close() }

// next reads the Adapter's next write.
func (x *transport) next() ([]byte, bool) {
	if !x.agent.Scan() {
		return nil, false
	}
	return x.agent.Bytes(), true
}

func (x *transport) abandon() {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.abandoned = true
}

func (x *transport) quiet() bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.abandoned
}

// wait blocks until the capture is played out.
func (x *transport) wait(t *testing.T) {
	t.Helper()
	select {
	case <-x.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the capture never played out")
	}
}
