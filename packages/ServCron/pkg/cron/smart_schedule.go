package cron

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type ScheduleSuggestion struct {
	JobID             string   `json:"job_id"`
	CurrentSchedule   string   `json:"current_schedule"`
	SuggestedSchedule string   `json:"suggested_schedule,omitempty"`
	Reason            string   `json:"reason"`
	ConflictJobs      []string `json:"conflict_jobs,omitempty"`
}

// AnalyzeSchedules checks the scheduler's jobs for scheduling conflicts (same intervals or cron schedules)
// and returns suggestions for resource optimization and conflict avoidance.
func (s *Scheduler) AnalyzeSchedules() []ScheduleSuggestion {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var suggestions []ScheduleSuggestion
	var activeJobs []*Job
	for _, job := range s.jobs {
		if job.Status == "active" {
			activeJobs = append(activeJobs, job)
		}
	}

	sort.Slice(activeJobs, func(i, j int) bool {
		return activeJobs[i].ID < activeJobs[j].ID
	})

	for i := 0; i < len(activeJobs); i++ {
		jobA := activeJobs[i]
		for j := i + 1; j < len(activeJobs); j++ {
			jobB := activeJobs[j]

			conflict := false
			reason := ""
			suggested := ""

			if jobA.Interval != "" && jobA.Interval == jobB.Interval {
				conflict = true
				reason = fmt.Sprintf("Job %s has the same execution interval (%s) as Job %s, causing resource spikes.", jobB.ID, jobB.Interval, jobA.ID)
				if strings.HasSuffix(jobB.Interval, "s") {
					val, err := time.ParseDuration(jobB.Interval)
					if err == nil {
						suggested = (val + 5*time.Second).String()
					} else {
						suggested = jobB.Interval + " (offset by 5s)"
					}
				} else {
					suggested = jobB.Interval + " (offset by 15s)"
				}
			} else if jobA.Cron != "" && jobA.Cron == jobB.Cron {
				conflict = true
				reason = fmt.Sprintf("Job %s runs on the exact same cron schedule (%s) as Job %s. This can cause resource contention.", jobB.ID, jobB.Cron, jobA.ID)
				if strings.HasPrefix(jobB.Cron, "0 ") {
					suggested = "5 " + strings.TrimPrefix(jobB.Cron, "0 ")
				} else if strings.HasPrefix(jobB.Cron, "*/") {
					suggested = jobB.Cron + " (with 10s delay)"
				} else {
					suggested = "1 " + jobB.Cron[2:]
				}
			}

			if conflict {
				currentSched := jobB.Interval
				if currentSched == "" {
					currentSched = jobB.Cron
				}
				suggestions = append(suggestions, ScheduleSuggestion{
					JobID:             jobB.ID,
					CurrentSchedule:   currentSched,
					SuggestedSchedule: suggested,
					Reason:            reason,
					ConflictJobs:      []string{jobA.ID},
				})
			}
		}
	}

	return suggestions
}
