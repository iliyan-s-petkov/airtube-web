package admit

import "testing"

// TestTryAcquireRefusesWhenFull is the property the whole package exists for:
// refusal must be immediate. A blocking acquire would just move the queue from
// pgxpool into this package.
func TestTryAcquireRefusesWhenFull(t *testing.T) {
	s, err := New(2)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	r1, ok := s.TryAcquire()
	if !ok {
		t.Fatal("first acquire failed on an empty semaphore")
	}
	r2, ok := s.TryAcquire()
	if !ok {
		t.Fatal("second acquire failed with a slot still free")
	}
	if _, ok := s.TryAcquire(); ok {
		t.Fatal("third acquire succeeded; the size of 2 is not being enforced")
	}
	if got := s.InFlight(); got != 2 {
		t.Errorf("InFlight = %d, want 2", got)
	}

	r1()
	if _, ok := s.TryAcquire(); !ok {
		t.Fatal("acquire failed after a release; the slot was not returned")
	}
	r2()
}

// TestReleaseIsIdempotent. A handler with an early return can plausibly call
// release twice. Crediting a slot twice would let the semaphore hand out more
// than `size` concurrent slots — a cap that silently stops capping is worse
// than no cap, because nothing looks wrong.
func TestReleaseIsIdempotent(t *testing.T) {
	s, err := New(1)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	release, ok := s.TryAcquire()
	if !ok {
		t.Fatal("acquire failed on an empty semaphore")
	}
	release()
	release()
	release()

	if got := s.InFlight(); got != 0 {
		t.Errorf("InFlight = %d, want 0", got)
	}
	if _, ok := s.TryAcquire(); !ok {
		t.Fatal("acquire failed after release")
	}
	if _, ok := s.TryAcquire(); ok {
		t.Fatal("a second slot was available; the repeated releases over-credited the semaphore")
	}
}

// TestNewRejectsNonPositiveSizes fails closed. A zero-sized semaphore refuses
// every request (a total outage) and a negative one is meaningless; both are
// configuration mistakes that must be caught at startup, where the message can
// name the variable.
func TestNewRejectsNonPositiveSizes(t *testing.T) {
	for _, size := range []int{0, -1} {
		if _, err := New(size); err == nil {
			t.Errorf("New(%d) returned no error", size)
		}
	}
}

// TestConcurrentAcquireNeverExceedsTheSize is the -race test. The counter and
// the slot channel must agree under contention, or the cap is advisory.
func TestConcurrentAcquireNeverExceedsTheSize(t *testing.T) {
	const size = 4
	s, err := New(size)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	start := make(chan struct{})
	done := make(chan struct{})
	for i := 0; i < 64; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			<-start
			release, ok := s.TryAcquire()
			if !ok {
				return
			}
			if n := s.InFlight(); n > size {
				t.Errorf("InFlight = %d, want <= %d", n, size)
			}
			release()
		}()
	}
	close(start)
	for i := 0; i < 64; i++ {
		<-done
	}
	if got := s.InFlight(); got != 0 {
		t.Errorf("InFlight = %d after all releases, want 0", got)
	}
}
