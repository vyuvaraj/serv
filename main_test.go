package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"servcron/pkg/cron"
	"servcron/pkg/server"
)

func TestSchedulerOperations(t *testing.T) {
	// Standalone/Mock leader function (always leader)
	isLeader := func() bool { return true }

	sched := cron.NewScheduler(isLeader)

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
	isLeader := func() bool {
		return atomic.LoadInt32(&isLeaderFlag) == 1
	}

	sched := cron.NewScheduler(isLeader)
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
	beforeTrigger := atomic.LoadInt64(&callCount)

	err = sched.TriggerJob("test-job")
	if err != nil {
		t.Fatalf("Failed to trigger job manually: %v", err)
	}

	// Wait for async task to process
	time.Sleep(50 * time.Millisecond)
	if atomic.LoadInt64(&callCount) != beforeTrigger+1 {
		t.Errorf("Manual trigger should increment executions by 1, got calls before: %d, after: %d", beforeTrigger, atomic.LoadInt64(&callCount))
	}
}

func TestServerRESTAPI(t *testing.T) {
	isLeader := func() bool { return true }
	sched := cron.NewScheduler(isLeader)
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
	sched := cron.NewScheduler(func() bool { return true })
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
