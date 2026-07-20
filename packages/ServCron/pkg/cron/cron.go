package cron

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vyuvaraj/ServShared"
)

// SyslogEmitFunc is overridable for testing or custom syslog transports.
var SyslogEmitFunc = func(entry map[string]interface{}) {
	logPath := os.Getenv("SERVCRON_EXEC_LOG_PATH")
	if logPath == "" {
		return // syslog integration not configured
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		log.Printf("[SERVCRON SYSLOG] failed to open log file %s: %v", logPath, err)
		return
	}
	defer f.Close()
	_, _ = f.Write(append(data, '\n'))
}

type Job struct {
	ID           string    `json:"id"`
	Interval     string    `json:"interval,omitempty"`  // duration string e.g. "10s", "1m"
	Cron         string    `json:"cron,omitempty"`      // standard 5-field cron e.g. "0 9 * * 1-5"
	TargetURL    string    `json:"target_url"`
	Payload      string    `json:"payload,omitempty"`
	NextTopic    string    `json:"next_topic,omitempty"`
	NextRun      time.Time `json:"next_run"`
	LastRun      time.Time `json:"last_run,omitempty"`
	Status       string    `json:"status"`              // "active", "paused"
	LastOutcome  string    `json:"last_outcome,omitempty"`
	FailureCount int       `json:"failure_count"`
}

type JobAuditLog struct {
	JobID        string    `json:"job_id"`
	Timestamp    time.Time `json:"timestamp"`
	DurationMs   int64     `json:"duration_ms"`
	StatusCode   int       `json:"status_code,omitempty"`
	Error        string    `json:"error,omitempty"`
	ResponseBody string    `json:"response_body,omitempty"`
}

type Scheduler struct {
	mu              sync.RWMutex
	jobs            map[string]*Job
	client          *http.Client
	stopChan        chan struct{}
	wg              sync.WaitGroup
	acquireLockFunc func(jobID string, nextRun time.Time) bool
	CheckInterval   time.Duration
	s3Endpoint      string
	s3Bucket        string
	s3AuthToken     string
}

func NewScheduler(acquireLockFunc func(jobID string, nextRun time.Time) bool) *Scheduler {
	endpoint := os.Getenv("SERV_STORE_ENDPOINT")
	bucket := os.Getenv("SERV_STORE_BUCKET")
	authToken := os.Getenv("SERV_STORE_AUTH_TOKEN")

	if endpoint == "" || authToken == "" {
		if raw := os.Getenv("SERVVERSE_DISCOVERY"); raw != "" {
			var manifest struct {
				Store     string `json:"store"`
				AuthToken string `json:"auth_token"`
			}
			if json.Unmarshal([]byte(raw), &manifest) == nil {
				if endpoint == "" && manifest.Store != "" {
					endpoint = manifest.Store
				}
				if authToken == "" && manifest.AuthToken != "" {
					authToken = manifest.AuthToken
				}
			}
		}
	}
	if endpoint == "" {
		endpoint = "http://localhost:8081"
	}
	if bucket == "" {
		bucket = "serv-cron"
	}
	if authToken == "" {
		authToken = "gateway-secret-token"
	}

	s := &Scheduler{
		jobs:            make(map[string]*Job),
		client:          &http.Client{Timeout: 10 * time.Second},
		stopChan:        make(chan struct{}),
		acquireLockFunc: acquireLockFunc,
		CheckInterval:   1 * time.Second,
		s3Endpoint:      strings.TrimSuffix(endpoint, "/"),
		s3Bucket:        bucket,
		s3AuthToken:     authToken,
	}

	s.loadJobsFromS3()
	return s
}

func (s *Scheduler) ensureBucketExists() {
	url := fmt.Sprintf("%s/%s", s.s3Endpoint, s.s3Bucket)
	req, err := http.NewRequest("PUT", url, nil)
	if err != nil {
		return
	}
	if s.s3AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.s3AuthToken)
	}
	resp, err := s.client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

func (s *Scheduler) loadJobsFromS3() {
	s.mu.Lock()
	defer s.mu.Unlock()

	url := fmt.Sprintf("%s/%s/jobs.json", s.s3Endpoint, s.s3Bucket)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return
	}
	if s.s3AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.s3AuthToken)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		var list []*Job
		if err := json.NewDecoder(resp.Body).Decode(&list); err == nil {
			for _, j := range list {
				s.jobs[j.ID] = j
			}
			log.Printf("Loaded %d jobs from S3 storage", len(list))
		}
	}
}

func (s *Scheduler) saveJobsToS3() {
	s.ensureBucketExists()

	s.mu.RLock()
	list := make([]*Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		list = append(list, j)
	}
	s.mu.RUnlock()

	url := fmt.Sprintf("%s/%s/jobs.json", s.s3Endpoint, s.s3Bucket)
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return
	}

	req, err := http.NewRequest("PUT", url, bytes.NewReader(data))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if s.s3AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.s3AuthToken)
	}

	resp, err := s.client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

func (s *Scheduler) saveAuditLogToS3(jobID string, startTime time.Time, durationMs int64, statusCode int, errStr string, respBody string) {
	s.ensureBucketExists()

	logEntry := JobAuditLog{
		JobID:        jobID,
		Timestamp:    startTime,
		DurationMs:   durationMs,
		StatusCode:   statusCode,
		Error:        errStr,
		ResponseBody: respBody,
	}

	data, err := json.MarshalIndent(logEntry, "", "  ")
	if err != nil {
		return
	}

	timestampStr := startTime.UTC().Format("20060102_150405_000")
	key := fmt.Sprintf("audit/%s_%s.json", jobID, timestampStr)
	url := fmt.Sprintf("%s/%s/%s", s.s3Endpoint, s.s3Bucket, key)

	req, err := http.NewRequest("PUT", url, bytes.NewReader(data))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if s.s3AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.s3AuthToken)
	}

	resp, err := s.client.Do(req)
	if err == nil {
		resp.Body.Close()
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
	if job.ID == "" {
		s.mu.Unlock()
		return fmt.Errorf("job ID cannot be empty")
	}
	if job.TargetURL == "" {
		s.mu.Unlock()
		return fmt.Errorf("target URL cannot be empty")
	}

	next, err := s.calculateNextRun(job, time.Now())
	if err != nil {
		s.mu.Unlock()
		return err
	}
	job.NextRun = next
	job.Status = "active"

	s.jobs[job.ID] = job
	s.mu.Unlock()

	log.Printf("Job '%s' registered. Next run: %v", job.ID, job.NextRun)
	go s.saveJobsToS3()
	return nil
}

func (s *Scheduler) GetJobs() []*Job {
	s.mu.RLock()
	defer s.mu.RUnlock()

	list := make([]*Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		copyJob := *j
		list = append(list, &copyJob)
	}
	return list
}

func (s *Scheduler) RemoveJob(id string) bool {
	s.mu.Lock()
	if _, ok := s.jobs[id]; ok {
		delete(s.jobs, id)
		s.mu.Unlock()
		log.Printf("Job '%s' removed", id)
		go s.saveJobsToS3()
		return true
	}
	s.mu.Unlock()
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

			if s.acquireLockFunc != nil && !s.acquireLockFunc(job.ID, job.NextRun) {
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

	span := ServShared.Span{
		TraceID:   traceID,
		SpanID:    spanID,
		Name:      fmt.Sprintf("servcron:TRIGGER %s", job.ID),
		Kind:      3,
		StartTime: time.Now().UnixNano(),
	}

	startTime := time.Now()
	resp, err := s.client.Do(req)
	duration := time.Since(startTime).Milliseconds()

	var statusCode int
	var errStr string
	var respBody string

	var attrs map[string]interface{}
	if err != nil {
		attrs = map[string]interface{}{"error": err.Error()}
		errStr = err.Error()
		log.Printf("Execution of job '%s' failed: %v", job.ID, err)
	} else {
		defer resp.Body.Close()
		statusCode = resp.StatusCode
		attrs = map[string]interface{}{"status_code": resp.StatusCode}
		log.Printf("Execution of job '%s' completed with status %d", job.ID, resp.StatusCode)
		
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		respBody = string(bodyBytes)
	}

	s.mu.Lock()
	if j, exists := s.jobs[job.ID]; exists {
		j.LastRun = startTime
		if err != nil || statusCode < 200 || statusCode >= 300 {
			j.LastOutcome = "failed"
			j.FailureCount++
		} else {
			j.LastOutcome = "success"
			j.FailureCount = 0
		}
	}
	s.mu.Unlock()

	ServShared.EndSpan(&span, err, attrs)

	// Emit structured syslog-style execution record
	outcome := "success"
	if err != nil || statusCode < 200 || statusCode >= 300 {
		outcome = "failed"
	}
	SyslogEmitFunc(map[string]interface{}{
		"timestamp":   startTime.UTC().Format("2006-01-02T15:04:05Z07:00"),
		"job_id":      job.ID,
		"target_url":  job.TargetURL,
		"duration_ms": duration,
		"status_code": statusCode,
		"outcome":     outcome,
		"error":       errStr,
	})

	go s.saveAuditLogToS3(job.ID, startTime, duration, statusCode, errStr, respBody)

	if job.NextTopic != "" && err == nil && statusCode >= 200 && statusCode < 300 {
		go s.publishToQueue(job.NextTopic, job.ID)
	}
}

func (s *Scheduler) publishToQueue(topic, jobID string) {
	queueURL := os.Getenv("SERV_QUEUE_URL")
	if queueURL == "" {
		if raw := os.Getenv("SERVVERSE_DISCOVERY"); raw != "" {
			var manifest struct {
				Queue string `json:"queue"`
			}
			if json.Unmarshal([]byte(raw), &manifest) == nil && manifest.Queue != "" {
				queueURL = manifest.Queue
			}
		}
	}
	if queueURL == "" {
		queueURL = "http://localhost:8082"
	}

	url := fmt.Sprintf("%s/api/v1/publish", strings.TrimSuffix(queueURL, "/"))
	payloadMap := map[string]interface{}{
		"topic":   topic,
		"payload": fmt.Sprintf(`{"job_id":%q,"status":"completed","timestamp":%q}`, jobID, time.Now().Format(time.RFC3339)),
	}
	bodyBytes, _ := json.Marshal(payloadMap)

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		log.Printf("[JOB_CHAIN] Failed to create request to ServQueue: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	authToken := os.Getenv("SERV_QUEUE_AUTH_TOKEN")
	if authToken == "" {
		if raw := os.Getenv("SERVVERSE_DISCOVERY"); raw != "" {
			var manifest struct {
				AuthToken string `json:"auth_token"`
			}
			if json.Unmarshal([]byte(raw), &manifest) == nil && manifest.AuthToken != "" {
				authToken = manifest.AuthToken
			}
		}
	}
	if authToken == "" {
		authToken = "secret-token"
	}
	req.Header.Set("Authorization", "Bearer "+authToken)

	resp, err := s.client.Do(req)
	if err != nil {
		log.Printf("[JOB_CHAIN] Failed to publish to ServQueue topic %s: %v", topic, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("[JOB_CHAIN] ServQueue returned status %d: %s", resp.StatusCode, string(body))
	} else {
		log.Printf("[JOB_CHAIN] Successfully triggered next job by publishing to topic %s on ServQueue", topic)
	}
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

func isNearestWeekday(t time.Time, targetDay int) bool {
	targetDate := time.Date(t.Year(), t.Month(), targetDay, 0, 0, 0, 0, t.Location())
	wd := targetDate.Weekday()
	var nearestDate time.Time
	if wd == time.Saturday {
		if targetDay == 1 {
			nearestDate = targetDate.AddDate(0, 0, 2)
		} else {
			nearestDate = targetDate.AddDate(0, 0, -1)
		}
	} else if wd == time.Sunday {
		lastDay := time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, t.Location()).Day()
		if targetDay == lastDay {
			nearestDate = targetDate.AddDate(0, 0, -2)
		} else {
			nearestDate = targetDate.AddDate(0, 0, 1)
		}
	} else {
		nearestDate = targetDate
	}
	return t.Year() == nearestDate.Year() && t.Month() == nearestDate.Month() && t.Day() == nearestDate.Day()
}

func matchCron(fields []string, t time.Time) bool {
	if len(fields) != 5 {
		return false
	}
	dayField := fields[2]
	var dayMatch bool
	if dayField == "L" {
		dayMatch = (t.AddDate(0, 0, 1).Day() == 1)
	} else if strings.HasSuffix(dayField, "W") {
		targetDayStr := strings.TrimSuffix(dayField, "W")
		targetDay := 1
		if targetDayStr != "" {
			if val, err := strconv.Atoi(targetDayStr); err == nil {
				targetDay = val
			}
		} else {
			targetDay = t.Day()
		}
		dayMatch = isNearestWeekday(t, targetDay)
	} else {
		dayMatch = matchField(dayField, t.Day(), 1, 31)
	}

	dowVal := int(t.Weekday())
	return matchField(fields[0], t.Minute(), 0, 59) &&
		matchField(fields[1], t.Hour(), 0, 23) &&
		dayMatch &&
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
