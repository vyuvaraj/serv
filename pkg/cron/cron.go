package cron

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vyuvaraj/ServShared"
)

type Job struct {
	ID        string    `json:"id"`
	Interval  string    `json:"interval,omitempty"`  // duration string e.g. "10s", "1m"
	Cron      string    `json:"cron,omitempty"`      // standard 5-field cron e.g. "0 9 * * 1-5"
	TargetURL string    `json:"target_url"`
	Payload   string    `json:"payload,omitempty"`
	NextRun   time.Time `json:"next_run"`
	LastRun   time.Time `json:"last_run,omitempty"`
	Status    string    `json:"status"`              // "active", "paused"
}

type Scheduler struct {
	mu              sync.RWMutex
	jobs            map[string]*Job
	client          *http.Client
	stopChan        chan struct{}
	wg              sync.WaitGroup
	acquireLockFunc func(jobID string, nextRun time.Time) bool
	CheckInterval   time.Duration
}

func NewScheduler(acquireLockFunc func(jobID string, nextRun time.Time) bool) *Scheduler {
	return &Scheduler{
		jobs:            make(map[string]*Job),
		client:          &http.Client{Timeout: 10 * time.Second},
		stopChan:        make(chan struct{}),
		acquireLockFunc: acquireLockFunc,
		CheckInterval:   1 * time.Second,
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
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for _, job := range s.jobs {
		if job.Status == "active" && now.After(job.NextRun) {
			next, err := s.calculateNextRun(job, now)
			if err != nil {
				log.Printf("Failed to calculate next run for job %s: %v", job.ID, err)
				job.Status = "paused"
				continue
			}

			// Attempt to acquire lock for this specific execution slot
			if s.acquireLockFunc != nil && !s.acquireLockFunc(job.ID, job.NextRun) {
				// Lock not acquired (another node is running it) — advance NextRun and skip
				job.NextRun = next
				continue
			}

			job.LastRun = now
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

	var attrs map[string]interface{}
	if err != nil {
		attrs = map[string]interface{}{"error": err.Error()}
		log.Printf("Execution of job '%s' failed: %v", job.ID, err)
	} else {
		defer resp.Body.Close()
		attrs = map[string]interface{}{"status_code": resp.StatusCode}
		log.Printf("Execution of job '%s' completed with status %d", job.ID, resp.StatusCode)
	}

	// Export span if telemetry is initialized
	ServShared.EndSpan(&span, err, attrs)
}

func (s *Scheduler) calculateNextRun(job *Job, from time.Time) (time.Time, error) {
	if job.Cron != "" {
		return CalculateNextCron(job.Cron, from)
	}

	if job.Interval == "" {
		return time.Time{}, fmt.Errorf("missing interval or cron configuration")
	}

	dur, err := time.ParseDuration(job.Interval)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid interval: %v", err)
	}

	return from.Add(dur), nil
}

func matchField(field string, val int, minVal, maxVal int) bool {
	if field == "*" {
		return true
	}
	if strings.Contains(field, ",") {
		parts := strings.Split(field, ",")
		for _, p := range parts {
			if matchField(p, val, minVal, maxVal) {
				return true
			}
		}
		return false
	}
	var step int = 1
	var rangeStr string = field
	if strings.Contains(field, "/") {
		parts := strings.Split(field, "/")
		rangeStr = parts[0]
		stepVal, err := strconv.Atoi(parts[1])
		if err == nil {
			step = stepVal
		}
	}
	var start, end int
	if rangeStr == "*" {
		start = minVal
		end = maxVal
	} else if strings.Contains(rangeStr, "-") {
		parts := strings.Split(rangeStr, "-")
		s, err1 := strconv.Atoi(parts[0])
		e, err2 := strconv.Atoi(parts[1])
		if err1 == nil && err2 == nil {
			start = s
			end = e
		}
	} else {
		s, err := strconv.Atoi(rangeStr)
		if err == nil {
			start = s
			end = s
		} else {
			return false
		}
	}

	for i := start; i <= end; i += step {
		if i == val {
			return true
		}
	}
	return false
}

func matchCron(fields []string, t time.Time) bool {
	if len(fields) != 5 {
		return false
	}
	dowVal := int(t.Weekday())
	return matchField(fields[0], t.Minute(), 0, 59) &&
		matchField(fields[1], t.Hour(), 0, 23) &&
		matchField(fields[2], t.Day(), 1, 31) &&
		matchField(fields[3], int(t.Month()), 1, 12) &&
		(matchField(fields[4], dowVal, 0, 6) || (dowVal == 0 && matchField(fields[4], 7, 0, 7)))
}

func CalculateNextCron(cronExpr string, from time.Time) (time.Time, error) {
	fields := strings.Fields(cronExpr)
	if len(fields) != 5 {
		return time.Time{}, fmt.Errorf("cron expression must have exactly 5 fields")
	}

	t := from.Truncate(time.Minute).Add(time.Minute)
	maxSearch := from.AddDate(2, 0, 0)
	for t.Before(maxSearch) {
		if matchCron(fields, t) {
			return t, nil
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}, fmt.Errorf("no match found in 2 years")
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
