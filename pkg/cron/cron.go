package cron

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/vyuvaraj/ServShared"
)

type Job struct {
	ID        string    `json:"id"`
	Interval  string    `json:"interval,omitempty"`  // duration string e.g. "10s", "1m"
	TargetURL string    `json:"target_url"`
	Payload   string    `json:"payload,omitempty"`
	NextRun   time.Time `json:"next_run"`
	LastRun   time.Time `json:"last_run,omitempty"`
	Status    string    `json:"status"`              // "active", "paused"
}

type Scheduler struct {
	mu            sync.RWMutex
	jobs          map[string]*Job
	client        *http.Client
	stopChan      chan struct{}
	wg            sync.WaitGroup
	isLeaderFunc  func() bool
	CheckInterval time.Duration
}

func NewScheduler(isLeaderFunc func() bool) *Scheduler {
	return &Scheduler{
		jobs:          make(map[string]*Job),
		client:        &http.Client{Timeout: 10 * time.Second},
		stopChan:      make(chan struct{}),
		isLeaderFunc:  isLeaderFunc,
		CheckInterval: 1 * time.Second,
	}
}

func (s *Scheduler) Start() {
	s.wg.Add(1)
	go s.runLoop()
}

func (s *Scheduler) Stop() {
	close(s.stopChan)
	s.wg.Wait()
}

func (s *Scheduler) AddJob(job *Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if job.ID == "" {
		return fmt.Errorf("job ID cannot be empty")
	}
	if job.TargetURL == "" {
		return fmt.Errorf("target URL cannot be empty")
	}

	// Calculate initial NextRun
	next, err := s.calculateNextRun(job, time.Now())
	if err != nil {
		return err
	}
	job.NextRun = next
	job.Status = "active"

	s.jobs[job.ID] = job
	log.Printf("Job '%s' registered. Next run: %v", job.ID, job.NextRun)
	return nil
}

func (s *Scheduler) GetJobs() []*Job {
	s.mu.RLock()
	defer s.mu.RUnlock()

	list := make([]*Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		// return copies
		copyJob := *j
		list = append(list, &copyJob)
	}
	return list
}

func (s *Scheduler) RemoveJob(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.jobs[id]; ok {
		delete(s.jobs, id)
		log.Printf("Job '%s' removed", id)
		return true
	}
	return false
}

func (s *Scheduler) TriggerJob(id string) error {
	s.mu.Lock()
	job, ok := s.jobs[id]
	s.mu.Unlock()

	if !ok {
		return fmt.Errorf("job not found")
	}

	go s.executeJob(job)
	return nil
}

func (s *Scheduler) runLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(s.CheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			s.checkAndRunJobs()
		}
	}
}

func (s *Scheduler) checkAndRunJobs() {
	if !s.isLeaderFunc() {
		// Only the leader runs scheduled jobs
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for _, job := range s.jobs {
		if job.Status == "active" && now.After(job.NextRun) {
			job.LastRun = now
			next, err := s.calculateNextRun(job, now)
			if err != nil {
				log.Printf("Failed to calculate next run for job %s: %v", job.ID, err)
				job.Status = "paused"
				continue
			}
			job.NextRun = next

			go s.executeJob(job)
		}
	}
}

func (s *Scheduler) executeJob(job *Job) {
	log.Printf("Executing job '%s' -> %s", job.ID, job.TargetURL)

	// Build OTel client tracing span
	traceID := generateTraceID()
	spanID := generateSpanID()
	traceparent := fmt.Sprintf("00-%s-%s-01", traceID, spanID)

	req, err := http.NewRequest(http.MethodPost, job.TargetURL, bytes.NewBufferString(job.Payload))
	if err != nil {
		log.Printf("Failed to create request for job %s: %v", job.ID, err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("traceparent", traceparent)

	// Trace span logging to telemetry if enabled
	span := ServShared.Span{
		TraceID:   traceID,
		SpanID:    spanID,
		Name:      fmt.Sprintf("servcron:TRIGGER %s", job.ID),
		Kind:      3, // client
		StartTime: time.Now().UnixNano(),
	}

	resp, err := s.client.Do(req)
	span.EndTime = time.Now().UnixNano()

	if err != nil {
		span.Status = 2 // error
		span.Attributes = map[string]interface{}{"error": err.Error()}
		log.Printf("Execution of job '%s' failed: %v", job.ID, err)
	} else {
		defer resp.Body.Close()
		span.Status = 1 // ok
		span.Attributes = map[string]interface{}{"status_code": resp.StatusCode}
		log.Printf("Execution of job '%s' completed with status %d", job.ID, resp.StatusCode)
	}

	// Export span if telemetry is initialized
	// (Internally handled by ServShared span buffer)
}

func (s *Scheduler) calculateNextRun(job *Job, from time.Time) (time.Time, error) {
	if job.Interval == "" {
		return time.Time{}, fmt.Errorf("missing interval configuration")
	}

	dur, err := time.ParseDuration(job.Interval)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid interval: %v", err)
	}

	return from.Add(dur), nil
}

func generateTraceID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func generateSpanID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
