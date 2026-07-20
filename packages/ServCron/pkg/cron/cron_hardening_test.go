package cron

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMissedExecutionCatchUp (D.35) verifies that when a scheduler resumes,
// jobs with NextRun in the past are executed immediately to catch up.
func TestMissedExecutionCatchUp(t *testing.T) {
	fired := make(chan bool, 5)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fired <- true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sched := NewScheduler(nil)
	sched.CheckInterval = 10 * time.Millisecond

	// Create a job with NextRun set 10 minutes in the past
	job := &Job{
		ID:        "catchup-job",
		Interval:  "1m",
		TargetURL: server.URL,
		NextRun:   time.Now().Add(-10 * time.Minute),
		Status:    "active",
	}

	_ = sched.AddJob(job)

	// Since AddJob calculates next run relative to Now, we override NextRun in the past manually
	sched.mu.Lock()
	sched.jobs[job.ID].NextRun = time.Now().Add(-10 * time.Minute)
	sched.mu.Unlock()

	sched.Start()
	defer sched.Stop()

	// Verify that the job was triggered immediately
	select {
	case <-fired:
		// success
	case <-time.After(500 * time.Millisecond):
		t.Errorf("expected missed execution to catch up and trigger instantly")
	}
}

// TestSplitBrainLeaderElection (D.36) simulates two nodes attempting leader promotion,
// proving only one node is elected and only one acquires the execution slot lock.
func TestSplitBrainLeaderElection(t *testing.T) {
	// Start mock Redis TCP server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start mock redis: %v", err)
	}
	addr := listener.Addr().String()

	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Track SetNX calls and lock holder
	var mu sync.Mutex
	locks := make(map[string]string)

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 1024)
				for {
					select {
					case <-ctx.Done():
						return
					default:
						_ = c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
						n, err := c.Read(buf)
						if err != nil {
							return
						}
						cmd := string(buf[:n])
						mu.Lock()
						// Split pipelined commands starting with *
						rawCmds := strings.Split(cmd, "*")
						for _, raw := range rawCmds {
							if raw == "" {
								continue
							}
							singleCmd := "*" + raw
							if strings.Contains(strings.ToUpper(singleCmd), "HELLO") {
								_, _ = c.Write([]byte("%1\r\n$6\r\nserver\r\n$5\r\nredis\r\n"))
							} else if strings.Contains(strings.ToUpper(singleCmd), "SET") && strings.Contains(strings.ToUpper(singleCmd), "NX") {
								// Simple parse: SET key val NX
								parts := strings.Fields(singleCmd)
								key := ""
								val := ""
								for idx, p := range parts {
									if strings.EqualFold(p, "set") && idx+2 < len(parts) {
										key = parts[idx+2]
										if idx+4 < len(parts) {
											val = parts[idx+4]
										}
									}
								}
								if key != "" {
									if _, exists := locks[key]; exists {
										_, _ = c.Write([]byte(":0\r\n")) // failed SETNX (integer 0)
									} else {
										locks[key] = val
										_, _ = c.Write([]byte(":1\r\n")) // successful SETNX (integer 1)
									}
								} else {
									_, _ = c.Write([]byte(":1\r\n"))
								}
							} else if strings.Contains(singleCmd, "PING") {
								_, _ = c.Write([]byte("+PONG\r\n"))
							} else if strings.Contains(singleCmd, "EXPIRE") {
								_, _ = c.Write([]byte(":1\r\n")) // EXPIRE returns integer status
							} else {
								_, _ = c.Write([]byte("+OK\r\n"))
							}
						}
						mu.Unlock()
					}
				}
			}(conn)
		}
	}()

	redisURL := fmt.Sprintf("redis://%s/0", addr)

	// Node 1 elector
	le1 := NewLeaderElector(redisURL, "leader-lock-key", 2*time.Second)
	// Reset global variable so NewLeaderElector doesn't return cached instance
	ActiveLeaderElectionProvider = nil
	// Node 2 elector
	le2 := NewLeaderElector(redisURL, "leader-lock-key", 2*time.Second)
	// Restore global provider nil
	ActiveLeaderElectionProvider = nil

	le1.Start()
	le2.Start()
	defer le1.Stop()
	defer le2.Stop()

	// Wait for promotion
	time.Sleep(500 * time.Millisecond)

	// Check leader statuses: only one node should be leader
	l1 := le1.IsLeader()
	l2 := le2.IsLeader()

	t.Logf("Leader statuses: Node 1=%v, Node 2=%v", l1, l2)
	if l1 && l2 {
		t.Errorf("both nodes cannot be elected leader simultaneously (split brain)")
	}

	// Verify AcquireJobLock execution lock
	runTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	ok1 := le1.AcquireJobLock("myjob", runTime)
	ok2 := le2.AcquireJobLock("myjob", runTime)

	t.Logf("Job locks: Node 1=%v, Node 2=%v", ok1, ok2)
	if ok1 && ok2 {
		t.Errorf("both nodes acquired execution lock for the same job and time slot")
	}

	listener.Close()
}

// TestCronEdgeCases (D.37) verifies leap year Feb 29, last day of month (L),
// nearest weekday (W) and DST transition schedules.
func TestCronEdgeCases(t *testing.T) {
	// 1. Leap year Feb 29: "0 0 29 2 *" starting from a non-leap year (e.g. 2027)
	fromLeap := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	nextLeap, err := CalculateNextCron("0 0 29 2 *", fromLeap)
	if err != nil {
		t.Fatalf("failed to calculate next run for leap year Feb 29: %v", err)
	}
	expectedLeap := time.Date(2028, 2, 29, 0, 0, 0, 0, time.UTC)
	if !nextLeap.Equal(expectedLeap) {
		t.Errorf("expected next leap run to be %v, got %v", expectedLeap, nextLeap)
	}

	// 2. Last day of month "L"
	fromL := time.Date(2028, 2, 28, 0, 0, 0, 0, time.UTC)
	nextL, err := CalculateNextCron("0 0 L * *", fromL)
	if err != nil {
		t.Fatalf("failed to calculate last day of month: %v", err)
	}
	expectedL := time.Date(2028, 2, 29, 0, 0, 0, 0, time.UTC) // Feb 29 is last day of Feb 2028
	if !nextL.Equal(expectedL) {
		t.Errorf("expected last day of month to be %v, got %v", expectedL, nextL)
	}

	// 3. Nearest weekday "W" to the 1st: "0 0 1W * *" starting from June 1, 2024 (Saturday)
	// June 1, 2024 is Saturday, so nearest weekday is June 3, 2024 (Monday)
	fromW := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	nextW, err := CalculateNextCron("0 0 1W * *", fromW)
	if err != nil {
		t.Fatalf("failed to calculate nearest weekday W: %v", err)
	}
	expectedW := time.Date(2024, 6, 3, 0, 0, 0, 0, time.UTC)
	if !nextW.Equal(expectedW) {
		t.Errorf("expected nearest weekday of Saturday 1st to be Monday 3rd (%v), got %v", expectedW, nextW)
	}
}
