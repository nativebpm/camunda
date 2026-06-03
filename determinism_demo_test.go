package camunda

import (
	"testing"
	"testing/synctest"
	"time"
)

// TestRealTimeDemo demonstrates how a regular test behaves when we attempt to
// wait for a long operation (e.g. 2 seconds timeout) to finish.
// We commented out the actual long execution to prevent slowing down project tests,
// but the logic shows the real overhead.
func TestRealTimeDemo(t *testing.T) {
	start := time.Now()

	// Simulate 100 milliseconds of real-time delay
	time.Sleep(100 * time.Millisecond)

	elapsed := time.Since(start)
	t.Logf("[Real Time] Real time elapsed: %v", elapsed)
}

// TestVirtualTimeDemo demonstrates the power of deterministic testing with synctest.
// We launch a goroutine that sleeps for 10 SECONDS (10000 ms).
// In a normal environment, this test would take 10 seconds. In the synctest bubble, it executes instantly!
func TestVirtualTimeDemo(t *testing.T) {
	// Measure the real execution time of the test outside the virtual time bubble
	realStart := time.Now()

	synctest.Test(t, func(t *testing.T) {
		virtualStart := time.Now()
		ch := make(chan struct{})

		// Start concurrent process in virtual time
		go func() {
			// Sleep for 10 seconds of virtual time
			time.Sleep(10 * time.Second)
			close(ch)
		}()

		// Simulate waiting in the main thread for the same 10 virtual seconds
		time.Sleep(10 * time.Second)
		<-ch

		t.Logf("[Synctest] Virtual time elapsed: %v", time.Since(virtualStart))
	})

	t.Logf("[Synctest] Real test CPU execution time: %v", time.Since(realStart))
}
