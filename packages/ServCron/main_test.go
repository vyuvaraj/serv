package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vyuvaraj/serv/packages/ServCron/pkg/cron"
	"github.com/vyuvaraj/serv/packages/ServCron/pkg/server"
)

func TestSchedulerOperations(t *testing.T) {
	sched := cron.NewScheduler(func(string, time.Time) bool { return true })

	job := &cron.Job{
		ID:        "job1",
		Interval:  "500ms",
		TargetURL: "http://localhost:9999/task",
		Payload:   `{"test": true}`,
	}

	// Test AddJob
	err := sched.AddJob(job)
	if err != nil {
		t.Fatalf("Failed to add job: %v", err)
	}

	// Test GetJobs
	jobs := sched.GetJobs()
	if len(jobs) != 1 || jobs[0].ID != "job1" {
		t.Errorf("Unexpected job list length or ID: %v", jobs)
	}

	// Test calculateNextRun calculations
	if jobs[0].NextRun.Before(time.Now()) {
		t.Errorf("NextRun should be in the future, got: %v", jobs[0].NextRun)
	}

	// Test RemoveJob
	ok := sched.RemoveJob("job1")
	if !ok {
		t.Errorf("Failed to remove job1")
	}

	if len(sched.GetJobs()) != 0 {
		t.Errorf("Jobs list should be empty after removal")
	}
}

func TestSchedulerEvictionAndTriggers(t *testing.T) {
	var callCount int64

	// Start test target server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&callCount, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	// Leader elector mock: Start as follower (IsLeader=false)
	var isLeaderFlag int32 = 0
	acquireLock := func(jobID string, nextRun time.Time) bool {
		return atomic.LoadInt32(&isLeaderFlag) == 1
	}

	sched := cron.NewScheduler(acquireLock)
	sched.CheckInterval = 50 * time.Millisecond
	sched.Start()
	defer sched.Stop()

	job := &cron.Job{
		ID:        "test-job",
		Interval:  "100ms",
		TargetURL: ts.URL,
	}

	err := sched.AddJob(job)
	if err != nil {
		t.Fatalf("Failed to add job: %v", err)
	}

	// Follower state: wait 300ms, should NOT run
	time.Sleep(300 * time.Millisecond)
	if atomic.LoadInt64(&callCount) > 0 {
		t.Errorf("Follower node should not execute scheduled jobs, but got callCount: %d", atomic.LoadInt64(&callCount))
	}

	// Promote to leader (IsLeader=true)
	atomic.StoreInt32(&isLeaderFlag, 1)

	// Wait for execution to trigger
	time.Sleep(350 * time.Millisecond)

	count := atomic.LoadInt64(&callCount)
	if count == 0 {
		t.Errorf("Leader node should have executed scheduled jobs, got callCount=0")
	}

	// Test manual TriggerJob (should work even if scheduler is follower)
	atomic.StoreInt32(&isLeaderFlag, 0)
	time.Sleep(150 * time.Millisecond) // Let in-flight requests finish
	beforeTrigger := atomic.LoadInt64(&callCount)

	err = sched.TriggerJob("test-job")
	if err != nil {
		t.Fatalf("Failed to trigger job manually: %v", err)
	}

	// Wait for async task to process
	time.Sleep(100 * time.Millisecond)
	if atomic.LoadInt64(&callCount) != beforeTrigger+1 {
		t.Errorf("Manual trigger should increment executions by 1, got calls before: %d, after: %d", beforeTrigger, atomic.LoadInt64(&callCount))
	}
}

func TestServerRESTAPI(t *testing.T) {
	sched := cron.NewScheduler(func(string, time.Time) bool { return true })
	elector := cron.NewLeaderElector("", "lock", 5*time.Second)

	srv := server.NewServer(sched, elector)
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	// Register test server
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// 1. Create a job via POST /api/jobs
	jobData := map[string]string{
		"id":         "api-job",
		"interval":   "5s",
		"target_url": "http://localhost:8080/task",
		"payload":    `{"data":"hello"}`,
	}
	body, _ := json.Marshal(jobData)

	resp, err := http.Post(ts.URL+"/api/jobs", "application/json", bytes.NewBuffer(body))
	if err != nil {
		t.Fatalf("Failed to execute POST /api/jobs: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Errorf("Expected status 201 Created, got: %d", resp.StatusCode)
	}

	// 2. Fetch jobs via GET /api/jobs
	resp2, err := http.Get(ts.URL + "/api/jobs")
	if err != nil {
		t.Fatalf("Failed to execute GET /api/jobs: %v", err)
	}
	defer resp2.Body.Close()

	var jobs []cron.Job
	json.NewDecoder(resp2.Body).Decode(&jobs)
	if len(jobs) != 1 || jobs[0].ID != "api-job" {
		t.Errorf("Expected 1 job named 'api-job', got: %v", jobs)
	}

	// 3. Trigger job manually via POST /api/jobs/{id}/run
	resp3, err := http.Post(ts.URL+"/api/jobs/api-job/run", "application/json", nil)
	if err != nil {
		t.Fatalf("Failed to POST trigger job: %v", err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got: %d", resp3.StatusCode)
	}

	// 4. Delete job via DELETE /api/jobs/{id}
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/jobs/api-job", nil)
	resp4, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Failed to DELETE job: %v", err)
	}
	defer resp4.Body.Close()
	if resp4.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got: %d", resp4.StatusCode)
	}

	// Verify empty list
	resp5, _ := http.Get(ts.URL + "/api/jobs")
	var jobsEmpty []cron.Job
	json.NewDecoder(resp5.Body).Decode(&jobsEmpty)
	if len(jobsEmpty) != 0 {
		t.Errorf("Expected job list to be empty after deletion, got: %d", len(jobsEmpty))
	}
}

func TestHealthProbeEndpoints(t *testing.T) {
	sched := cron.NewScheduler(func(string, time.Time) bool { return true })
	elector := cron.NewLeaderElector("", "lock", 5*time.Second)

	srv := server.NewServer(sched, elector)
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Test health check endpoint
	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("Failed to check /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected health status 200, got: %d", resp.StatusCode)
	}

	data, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(data, []byte("healthy")) {
		t.Errorf("Expected health response to contain 'healthy', got: %s", string(data))
	}
}

func TestCronSyntaxParsing(t *testing.T) {
	from := time.Date(2026, 6, 23, 9, 0, 0, 0, time.UTC)

	// Case 1: Standard Cron (Every day at 9:05 AM)
	next, err := cron.CalculateNextCron("5 9 * * *", from)
	if err != nil {
		t.Fatalf("Failed to calculate next cron: %v", err)
	}
	expected := time.Date(2026, 6, 23, 9, 5, 0, 0, time.UTC)
	if !next.Equal(expected) {
		t.Errorf("Expected %v, got %v", expected, next)
	}

	// Case 2: Range & Step (Every 10 minutes between 9:00 and 9:30)
	next, err = cron.CalculateNextCron("*/10 9 * * *", from)
	if err != nil {
		t.Fatalf("Failed: %v", err)
	}
	expected = time.Date(2026, 6, 23, 9, 10, 0, 0, time.UTC)
	if !next.Equal(expected) {
		t.Errorf("Expected %v, got %v", expected, next)
	}

	// Case 3: List (At 9:15 and 9:45)
	next, err = cron.CalculateNextCron("15,45 9 * * *", from)
	if err != nil {
		t.Fatalf("Failed: %v", err)
	}
	expected = time.Date(2026, 6, 23, 9, 15, 0, 0, time.UTC)
	if !next.Equal(expected) {
		t.Errorf("Expected %v, got %v", expected, next)
	}
}

func TestDynamicLoadBalancing(t *testing.T) {
	// Mock shared lock store with mutex to prevent concurrent map writes
	var mu sync.Mutex
	locks := make(map[string]string) // slotKey -> nodeID
	
	acquireLockMock := func(nodeID string) func(string, time.Time) bool {
		return func(jobID string, nextRun time.Time) bool {
			mu.Lock()
			defer mu.Unlock()
			slotKey := fmt.Sprintf("%s:%d", jobID, nextRun.Unix())
			if _, exists := locks[slotKey]; exists {
				return false
			}
			locks[slotKey] = nodeID
			return true
		}
	}

	var executions int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&executions, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	// Start two schedulers representing two nodes
	sched1 := cron.NewScheduler(acquireLockMock("node-1"))
	sched1.CheckInterval = 50 * time.Millisecond
	sched1.Start()
	defer sched1.Stop()

	sched2 := cron.NewScheduler(acquireLockMock("node-2"))
	sched2.CheckInterval = 50 * time.Millisecond
	sched2.Start()
	defer sched2.Stop()

	job := &cron.Job{
		ID:        "balanced-job",
		Interval:  "100ms",
		TargetURL: ts.URL,
	}

	// Register job on both nodes
	sched1.AddJob(job)
	sched2.AddJob(job)

	// Let them run for 300ms
	time.Sleep(300 * time.Millisecond)

	// Since they compete, only one scheduler should run each execution slot
	execs := atomic.LoadInt64(&executions)
	if execs == 0 {
		t.Errorf("Expected jobs to execute, got 0 executions")
	}

	// Let's verify that the total executions match the number of unique locks created
	if int(execs) != len(locks) {
		t.Errorf("Executions (%d) do not match lock count (%d)", execs, len(locks))
	}
}

func TestS3PersistenceAndAuditLog(t *testing.T) {
	var gotPutJob, gotPutAudit bool
	var putAuditData string

	// 1. Start a mock ServStore S3 server
	s3Mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			if r.URL.Path == "/serv-cron/jobs.json" {
				gotPutJob = true
				w.WriteHeader(http.StatusOK)
				return
			}
			if strings.HasPrefix(r.URL.Path, "/serv-cron/audit/") {
				gotPutAudit = true
				bodyBytes, _ := io.ReadAll(r.Body)
				putAuditData = string(bodyBytes)
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == "GET" && r.URL.Path == "/serv-cron/jobs.json" {
			// Return an empty list of jobs initially
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("[]"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer s3Mock.Close()

	// 2. Set environment variables to point to mock S3
	t.Setenv("SERV_STORE_ENDPOINT", s3Mock.URL)
	t.Setenv("SERV_STORE_BUCKET", "serv-cron")
	t.Setenv("SERV_STORE_AUTH_TOKEN", "mock-token")

	// 3. Create a scheduler
	sched := cron.NewScheduler(func(string, time.Time) bool { return true })

	// 4. Add a job -> check that saveJobsToS3 is called
	job := &cron.Job{
		ID:        "s3-test-job",
		Interval:  "1m",
		TargetURL: "http://localhost:9999/task",
	}
	err := sched.AddJob(job)
	if err != nil {
		t.Fatalf("Failed to add job: %v", err)
	}

	// Wait for background go routine saveJobsToS3
	time.Sleep(100 * time.Millisecond)
	if !gotPutJob {
		t.Errorf("Expected job registration to trigger a PUT call to S3 jobs.json")
	}

	// 5. Trigger job execution -> check that audit log is saved
	// We'll set up a target server that the job calls
	targetCallCount := 0
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCallCount++
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("api response body"))
	}))
	defer targetServer.Close()

	job.TargetURL = targetServer.URL
	err = sched.TriggerJob("s3-test-job")
	if err != nil {
		t.Fatalf("Failed to trigger job: %v", err)
	}

	// Wait for execution and background saveAuditLogToS3
	time.Sleep(150 * time.Millisecond)

	if targetCallCount != 1 {
		t.Errorf("Expected job target server to be called once, got %d", targetCallCount)
	}
	if !gotPutAudit {
		t.Errorf("Expected job execution to trigger a PUT call to S3 audit logs")
	}

	// Verify content of audit log
	if !strings.Contains(putAuditData, "s3-test-job") {
		t.Errorf("Audit log should contain Job ID, got: %s", putAuditData)
	}
	if !strings.Contains(putAuditData, "api response body") {
		t.Errorf("Audit log should contain response body, got: %s", putAuditData)
	}
}

func BenchmarkCronNextCalculation(b *testing.B) {
	// Typical cron expressions of varying complexity
	exprs := []string{
		"* * * * *",       // every minute
		"0 9 * * 1-5",    // weekdays at 9am
		"*/15 * * * *",   // every 15 minutes
		"0 0 1 * *",      // first of each month
		"30 18 * * 5",    // friday 6:30pm
	}
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < b.N; i++ {
		expr := exprs[i%len(exprs)]
		_, _ = cron.CalculateNextCron(expr, from)
	}
}

func TestSchedulerOutcomeTracking(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	sched := cron.NewScheduler(func(string, time.Time) bool { return true })
	sched.CheckInterval = 50 * time.Millisecond
	job := &cron.Job{
		ID:        "outcome-job",
		Interval:  "50ms",
		TargetURL: server.URL,
	}

	err := sched.AddJob(job)
	if err != nil {
		t.Fatalf("Failed to add job: %v", err)
	}

	sched.Start()
	time.Sleep(300 * time.Millisecond)
	sched.Stop()

	jobs := sched.GetJobs()
	if len(jobs) != 1 {
		t.Fatalf("Expected 1 job, got %d", len(jobs))
	}
	if jobs[0].LastOutcome != "success" {
		t.Errorf("Expected LastOutcome to be 'success', got %s", jobs[0].LastOutcome)
	}
	if jobs[0].FailureCount != 0 {
		t.Errorf("Expected FailureCount to be 0, got %d", jobs[0].FailureCount)
	}
}

