package cron

import (
	"testing"
)

func TestSmartScheduleIntervalConflict(t *testing.T) {
	scheduler := &Scheduler{
		jobs: make(map[string]*Job),
	}

	// 2 jobs with same interval "10s"
	job1 := &Job{
		ID:        "job-1",
		Interval:  "10s",
		TargetURL: "http://localhost:8080",
		Status:    "active",
	}
	job2 := &Job{
		ID:        "job-2",
		Interval:  "10s",
		TargetURL: "http://localhost:8080",
		Status:    "active",
	}

	scheduler.jobs[job1.ID] = job1
	scheduler.jobs[job2.ID] = job2

	suggestions := scheduler.AnalyzeSchedules()

	if len(suggestions) != 1 {
		t.Fatalf("Expected 1 suggestion, got %d", len(suggestions))
	}

	sug := suggestions[0]
	if sug.JobID != "job-2" {
		t.Errorf("Expected suggestion for job-2, got %s", sug.JobID)
	}
	if sug.SuggestedSchedule != "15s" {
		t.Errorf("Expected suggested schedule 15s, got %s", sug.SuggestedSchedule)
	}
}

func TestSmartScheduleCronConflict(t *testing.T) {
	scheduler := &Scheduler{
		jobs: make(map[string]*Job),
	}

	// 2 jobs with same cron "0 9 * * 1-5"
	job1 := &Job{
		ID:        "job-1",
		Cron:      "0 9 * * 1-5",
		TargetURL: "http://localhost:8080",
		Status:    "active",
	}
	job2 := &Job{
		ID:        "job-2",
		Cron:      "0 9 * * 1-5",
		TargetURL: "http://localhost:8080",
		Status:    "active",
	}

	scheduler.jobs[job1.ID] = job1
	scheduler.jobs[job2.ID] = job2

	suggestions := scheduler.AnalyzeSchedules()

	if len(suggestions) != 1 {
		t.Fatalf("Expected 1 suggestion, got %d", len(suggestions))
	}

	sug := suggestions[0]
	if sug.SuggestedSchedule != "5 9 * * 1-5" {
		t.Errorf("Expected suggested schedule '5 9 * * 1-5', got %q", sug.SuggestedSchedule)
	}
}
