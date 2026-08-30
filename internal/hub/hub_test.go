package hub

import (
	"sync"
	"testing"
	"time"
)

// TestUnregisterClosesSendChannel exercises the contract handleWS depends
// on: once a client is unregistered, reading its Send() channel must report
// closed (ok=false) rather than block, or handleWS's receive loop would
// never notice the client is gone.
func TestUnregisterClosesSendChannel(t *testing.T) {
	h := New()
	c := h.Register()
	h.Unregister(c)

	select {
	case _, ok := <-c.Send():
		if ok {
			t.Fatal("Send() delivered a value after Unregister; want a closed channel (ok=false)")
		}
	case <-time.After(time.Second):
		t.Fatal("reading Send() after Unregister blocked instead of reporting closed")
	}
}

// TestUnregisterIsIdempotent guards the map-membership check in Unregister:
// without it, a second Unregister call on the same client would close an
// already-closed channel and panic.
func TestUnregisterIsIdempotent(t *testing.T) {
	h := New()
	c := h.Register()
	h.Unregister(c)
	h.Unregister(c) // must not panic
}

// TestBroadcastAfterUnregisterDoesNotPanic guards the ordering inside
// Unregister: it must remove the client from the map before closing its
// channel, or a Broadcast racing in afterward could still find the client
// and try to send on its now-closed channel, which panics.
func TestBroadcastAfterUnregisterDoesNotPanic(t *testing.T) {
	h := New()
	c := h.Register()
	h.Unregister(c)
	h.Broadcast([]byte("msg")) // must not panic
}

// TestBroadcastSkipsFullClientWithoutBlocking is the regression test for
// Broadcast's documented behavior: a client whose send buffer is already
// full gets that message dropped instead of blocking the whole fan-out.
// "slow" is filled to capacity with no reader at all, standing in for a
// stuck browser tab; "fast" joins only afterward, standing in for every
// other connected tab, which must not be affected by "slow" being full.
// Filling slow first, before fast even exists, keeps this deterministic —
// there's no reader to race against the fill, and fast's own buffer has
// far more room than the handful of messages it's asked to hold, so
// nothing here depends on goroutine scheduling order.
func TestBroadcastSkipsFullClientWithoutBlocking(t *testing.T) {
	h := New()
	slow := h.Register() // never drained

	// No reader is racing this, so it can't block: sendBuffer non-blocking
	// sends into a buffer with exactly that much room always succeed.
	for i := 0; i < sendBuffer; i++ {
		h.Broadcast([]byte("fill"))
	}

	fast := h.Register() // joins only once slow is already full

	// More broadcasts than slow's buffer has room for: the regression
	// target is that this doesn't block on slow's overflow, and that it
	// still reaches fast.
	const extra = 5
	done := make(chan struct{})
	go func() {
		for i := 0; i < extra; i++ {
			h.Broadcast([]byte("overflow"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Broadcast blocked on a full client instead of dropping its overflow")
	}

	// fast has plenty of room for `extra` messages, so it must have
	// received all of them — a full sibling client must not affect
	// delivery to anyone else.
	for i := 0; i < extra; i++ {
		select {
		case <-fast.Send():
		case <-time.After(time.Second):
			t.Fatalf("fast client only received %d of %d broadcasts sent while a sibling client's buffer was full", i, extra)
		}
	}

	// slow's buffer should hold exactly sendBuffer messages — the "fill"
	// batch, with every "overflow" message dropped rather than queued.
	for i := 0; i < sendBuffer; i++ {
		select {
		case <-slow.Send():
		default:
			t.Fatalf("slow client only received %d of %d buffered messages", i, sendBuffer)
		}
	}
	select {
	case _, ok := <-slow.Send():
		if ok {
			t.Fatal("slow client's buffer held more than sendBuffer messages; overflow should have been dropped")
		}
	default:
	}
}

// TestConcurrentRegisterUnregisterBroadcast is the concurrency test for the
// one genuinely concurrent piece of state in this codebase: h.clients,
// guarded by h.mu. It compresses the real handleWS lifecycle (Register,
// read Send() in a loop, Unregister on disconnect) into many goroutines
// joining and leaving while other goroutines hammer Broadcast, which
// iterates and sends to the same map. None of this should race under
// `go test -race`, and it should never deadlock — Broadcast, Register, and
// Unregister all take h.mu only briefly and never block while holding it
// (Broadcast's send to each client is a non-blocking select).
func TestConcurrentRegisterUnregisterBroadcast(t *testing.T) {
	h := New()
	const (
		numClients             = 20
		cyclesPerClient        = 25
		numBroadcasters        = 4
		broadcastsPerGoroutine = 200
	)

	var wg sync.WaitGroup

	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for cycle := 0; cycle < cyclesPerClient; cycle++ {
				c := h.Register()
				// Touch Send() alongside concurrent Broadcasts without
				// blocking indefinitely — a real reader loop keeps going
				// until Unregister closes the channel; this just needs
				// to race against that, not fully model it.
				select {
				case <-c.Send():
				case <-time.After(time.Millisecond):
				}
				h.Unregister(c)
			}
		}()
	}

	for i := 0; i < numBroadcasters; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < broadcastsPerGoroutine; j++ {
				h.Broadcast([]byte("msg"))
			}
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent Register/Unregister/Broadcast did not finish — likely deadlocked")
	}
}
