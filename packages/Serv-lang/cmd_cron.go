package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type CronJob struct {
	ID           string    `json:"id"`
	Interval     string    `json:"interval,omitempty"`
	Cron         string    `json:"cron,omitempty"`
	TargetURL    string    `json:"target_url"`
	Payload      string    `json:"payload,omitempty"`
	NextTopic    string    `json:"next_topic,omitempty"`
	NextRun      time.Time `json:"next_run"`
	LastRun      time.Time `json:"last_run,omitempty"`
	Status       string    `json:"status"`
	LastOutcome  string    `json:"last_outcome,omitempty"`
	FailureCount int       `json:"failure_count"`
}

func runCronList() {
	host := "http://localhost:8080"
	if envHost := os.Getenv("SERV_CRON_URL"); envHost != "" {
		host = envHost
	}

	for i := 2; i < len(os.Args); i++ {
		if (os.Args[i] == "--host" || os.Args[i] == "-host") && i+1 < len(os.Args) {
			host = os.Args[i+1]
			i++
		}
	}

	url := fmt.Sprintf("%s/api/v1/jobs", strings.TrimSuffix(host, "/"))
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		fmt.Printf("Failed to create request: %v\n", err)
		os.Exit(1)
	}

	authToken := os.Getenv("SERV_CRON_AUTH_TOKEN")
	if authToken == "" {
		authToken = "secret-token"
	}
	req.Header.Set("Authorization", "Bearer "+authToken)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Failed to connect to ServCron at %s: %v\n", host, err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("ServCron returned status %d: %s\n", resp.StatusCode, string(body))
		os.Exit(1)
	}

	var jobs []CronJob
	if err := json.NewDecoder(resp.Body).Decode(&jobs); err != nil {
		fmt.Printf("Failed to decode response: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("=== ServCron Scheduled Jobs ===")
	if len(jobs) == 0 {
		fmt.Println("  No registered jobs.")
		return
	}

	for _, job := range jobs {
		fmt.Printf("\nJob ID:       %s\n", job.ID)
		fmt.Printf("Status:       %s\n", job.Status)
		fmt.Printf("Target:       %s\n", job.TargetURL)
		scheduleStr := ""
		if job.Cron != "" {
			scheduleStr = fmt.Sprintf("Cron (%s)", job.Cron)
		} else {
			scheduleStr = fmt.Sprintf("Interval (%s)", job.Interval)
		}
		fmt.Printf("Schedule:     %s\n", scheduleStr)
		if !job.LastRun.IsZero() {
			outcome := "unknown"
			if job.LastOutcome != "" {
				outcome = job.LastOutcome
			}
			fmt.Printf("Last Run:     %s (%s)\n", job.LastRun.Format("2006-01-02 15:04:05"), outcome)
		}
		if job.FailureCount > 0 {
			fmt.Printf("Failures:     %d consecutive failure(s)\n", job.FailureCount)
		}

		nextRuns := projectNext5Runs(job, time.Now())
		fmt.Println("Next 5 Runs:")
		for idx, nextT := range nextRuns {
			fmt.Printf("  %d. %s\n", idx+1, nextT.Format("2006-01-02 15:04:05"))
		}
		fmt.Println("----------------------------------------")
	}
}

func projectNext5Runs(job CronJob, start time.Time) []time.Time {
	var nextRuns []time.Time
	curr := start
	for len(nextRuns) < 5 {
		nextT, err := calculateNextRunLocal(job, curr)
		if err != nil {
			break
		}
		nextRuns = append(nextRuns, nextT)
		curr = nextT
	}
	return nextRuns
}

func calculateNextRunLocal(job CronJob, from time.Time) (time.Time, error) {
	if job.Cron != "" {
		return CalculateNextCronLocal(job.Cron, from)
	}
	if job.Interval == "" {
		return time.Time{}, fmt.Errorf("missing schedule")
	}
	dur, err := time.ParseDuration(job.Interval)
	if err != nil {
		return time.Time{}, err
	}
	return from.Add(dur), nil
}

func matchFieldLocal(field string, val int, minVal, maxVal int) bool {
	if field == "*" {
		return true
	}
	if strings.Contains(field, ",") {
		parts := strings.Split(field, ",")
		for _, p := range parts {
			if matchFieldLocal(p, val, minVal, maxVal) {
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

func matchCronLocal(fields []string, t time.Time) bool {
	if len(fields) != 5 {
		return false
	}
	dowVal := int(t.Weekday())
	return matchFieldLocal(fields[0], t.Minute(), 0, 59) &&
		matchFieldLocal(fields[1], t.Hour(), 0, 23) &&
		matchFieldLocal(fields[2], t.Day(), 1, 31) &&
		matchFieldLocal(fields[3], int(t.Month()), 1, 12) &&
		(matchFieldLocal(fields[4], dowVal, 0, 6) || (dowVal == 0 && matchFieldLocal(fields[4], 7, 0, 7)))
}

func CalculateNextCronLocal(cronExpr string, from time.Time) (time.Time, error) {
	fields := strings.Fields(cronExpr)
	if len(fields) != 5 {
		return time.Time{}, fmt.Errorf("cron expression must have exactly 5 fields")
	}

	t := from.Truncate(time.Minute).Add(time.Minute)
	maxSearch := from.AddDate(2, 0, 0)
	for t.Before(maxSearch) {
		if matchCronLocal(fields, t) {
			return t, nil
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}, fmt.Errorf("no run time found in 2 years")
}
