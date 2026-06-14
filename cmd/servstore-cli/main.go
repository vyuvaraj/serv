package main

import (
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

const usage = `ServStore CLI — manage buckets and objects from the terminal.

Usage:
  servstore-cli [global flags] <command> [args]

Global flags:
  --endpoint    ServStore base URL (default: http://localhost:8080)
  --access-key  Access key ID        (default: minioadmin)
  --secret-key  Secret access key    (default: minioadmin)

Commands:
  mb   <bucket>                    Create a bucket
  rb   <bucket>                    Remove a bucket
  ls   [bucket [prefix]]           List buckets or objects in a bucket
  put  <bucket> <key> <file>       Upload a file as an object
  get  <bucket> <key> <dest>       Download an object to a file (use - for stdout)
  rm   <bucket> <key>              Delete an object
  lock <bucket> <key> <duration>   Apply a WORM lock (e.g. 72h, 30d, 1y)
  lc-set     <bucket> <days> [prefix] Set lifecycle expiry rule (delete objects older than N days)
  lc-get     <bucket>                 Show the lifecycle configuration for a bucket
  lc-del     <bucket>                 Remove the lifecycle configuration from a bucket
  policy-set     <username> <file.json> Attach an IAM policy to a user
  policy-get     <username>             View the attached IAM policy for a user
  policy-del     <username>             Delete the attached IAM policy for a user
  cluster-status                        View the status of all nodes in the cluster
  placement  <bucket> <key>                Find the node owning a specific key
  help                                  Print this message
`

// ---------- global flags ----------

var (
	endpoint  string
	accessKey string
	secretKey string
)

func main() {
	flag.StringVar(&endpoint, "endpoint", "http://localhost:8080", "ServStore base URL")
	flag.StringVar(&accessKey, "access-key", "minioadmin", "Access key ID")
	flag.StringVar(&secretKey, "secret-key", "minioadmin", "Secret access key")
	flag.Usage = func() { fmt.Print(usage) }
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		fmt.Print(usage)
		os.Exit(0)
	}

	cmd := args[0]
	rest := args[1:]

	var err error
	switch cmd {
	case "mb":
		err = cmdMB(rest)
	case "rb":
		err = cmdRB(rest)
	case "ls":
		err = cmdLS(rest)
	case "put":
		err = cmdPut(rest)
	case "get":
		err = cmdGet(rest)
	case "rm":
		err = cmdRM(rest)
	case "lock":
		err = cmdLock(rest)
	case "lc-set":
		err = cmdLCSet(rest)
	case "lc-get":
		err = cmdLCGet(rest)
	case "lc-del":
		err = cmdLCDel(rest)
	case "policy-set":
		err = cmdPolicySet(rest)
	case "policy-get":
		err = cmdPolicyGet(rest)
	case "policy-del":
		err = cmdPolicyDel(rest)
	case "cluster-status":
		err = cmdClusterStatus(rest)
	case "placement":
		err = cmdPlacement(rest)
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fatalf("unknown command %q — run 'servstore-cli help' for usage\n", cmd)
	}

	if err != nil {
		fatalf("%v\n", err)
	}
}

// ---------- commands ----------

func cmdMB(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: mb <bucket>")
	}
	bucket := args[0]
	req, _ := http.NewRequest(http.MethodPut, url("/"+bucket), nil)
	resp, err := do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := expectStatus(resp, http.StatusOK, http.StatusNoContent); err != nil {
		return err
	}
	fmt.Printf("Bucket '%s' created.\n", bucket)
	return nil
}

func cmdRB(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: rb <bucket>")
	}
	bucket := args[0]
	req, _ := http.NewRequest(http.MethodDelete, url("/"+bucket), nil)
	resp, err := do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := expectStatus(resp, http.StatusOK, http.StatusNoContent); err != nil {
		return err
	}
	fmt.Printf("Bucket '%s' removed.\n", bucket)
	return nil
}

// S3 ListAllMyBuckets response
type listBucketsResult struct {
	Buckets []struct {
		Name         string `xml:"Name"`
		CreationDate string `xml:"CreationDate"`
	} `xml:"Buckets>Bucket"`
}

// S3 ListObjects response
type listObjectsResult struct {
	Name     string `xml:"Name"`
	Contents []struct {
		Key          string `xml:"Key"`
		Size         int64  `xml:"Size"`
		LastModified string `xml:"LastModified"`
		ETag         string `xml:"ETag"`
	} `xml:"Contents"`
	CommonPrefixes []struct {
		Prefix string `xml:"Prefix"`
	} `xml:"CommonPrefixes"`
}

func cmdLS(args []string) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	defer w.Flush()

	if len(args) == 0 {
		// List buckets
		req, _ := http.NewRequest(http.MethodGet, url("/"), nil)
		resp, err := do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if err := expectStatus(resp, http.StatusOK); err != nil {
			return err
		}
		var result listBucketsResult
		if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}
		if len(result.Buckets) == 0 {
			fmt.Println("(no buckets)")
			return nil
		}
		fmt.Fprintln(w, "BUCKET\tCREATED")
		for _, b := range result.Buckets {
			fmt.Fprintf(w, "%s\t%s\n", b.Name, formatDate(b.CreationDate))
		}
		return nil
	}

	// List objects in bucket
	bucket := args[0]
	prefix := ""
	if len(args) > 1 {
		prefix = args[1]
	}
	qp := "?delimiter=/&max-keys=1000"
	if prefix != "" {
		qp += "&prefix=" + prefix
	}
	req, _ := http.NewRequest(http.MethodGet, url("/"+bucket+"/"+qp), nil)
	resp, err := do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := expectStatus(resp, http.StatusOK); err != nil {
		return err
	}
	var result listObjectsResult
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	if len(result.CommonPrefixes) == 0 && len(result.Contents) == 0 {
		fmt.Println("(empty)")
		return nil
	}
	fmt.Fprintln(w, "TYPE\tSIZE\tLAST MODIFIED\tKEY/PREFIX")
	for _, cp := range result.CommonPrefixes {
		fmt.Fprintf(w, "DIR\t-\t-\t%s\n", cp.Prefix)
	}
	for _, obj := range result.Contents {
		fmt.Fprintf(w, "OBJ\t%d\t%s\t%s\n", obj.Size, formatDate(obj.LastModified), obj.Key)
	}
	return nil
}

func cmdPut(args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("usage: put <bucket> <key> <file>")
	}
	bucket, key, filePath := args[0], args[1], args[2]

	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return err
	}

	req, _ := http.NewRequest(http.MethodPut, url("/"+bucket+"/"+key), f)
	req.ContentLength = fi.Size()
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := expectStatus(resp, http.StatusOK, http.StatusNoContent, http.StatusCreated); err != nil {
		return err
	}
	etag := strings.Trim(resp.Header.Get("ETag"), `"`)
	fmt.Printf("Uploaded '%s' → %s/%s", filePath, bucket, key)
	if etag != "" {
		fmt.Printf("  (ETag: %s)", etag)
	}
	fmt.Println()
	return nil
}

func cmdGet(args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("usage: get <bucket> <key> <dest|-|>")
	}
	bucket, key, dest := args[0], args[1], args[2]

	req, _ := http.NewRequest(http.MethodGet, url("/"+bucket+"/"+key), nil)
	resp, err := do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := expectStatus(resp, http.StatusOK); err != nil {
		return err
	}

	var out io.Writer
	if dest == "-" {
		out = os.Stdout
	} else {
		f, err := os.Create(dest)
		if err != nil {
			return fmt.Errorf("create file: %w", err)
		}
		defer f.Close()
		out = f
	}

	n, err := io.Copy(out, resp.Body)
	if err != nil {
		return err
	}
	if dest != "-" {
		fmt.Printf("Downloaded %s/%s → %s  (%d bytes)\n", bucket, key, dest, n)
	}
	return nil
}

func cmdRM(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: rm <bucket> <key>")
	}
	bucket, key := args[0], args[1]
	req, _ := http.NewRequest(http.MethodDelete, url("/"+bucket+"/"+key), nil)
	resp, err := do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := expectStatus(resp, http.StatusOK, http.StatusNoContent); err != nil {
		return err
	}
	fmt.Printf("Deleted %s/%s\n", bucket, key)
	return nil
}

func cmdLock(args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("usage: lock <bucket> <key> <duration>  (e.g. 72h, 30d, 1y)")
	}
	bucket, key, rawDur := args[0], args[1], args[2]

	// Support day (d) and year (y) suffixes in addition to Go duration syntax
	retainUntil, err := parseDuration(rawDur)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", rawDur, err)
	}

	retainStr := retainUntil.UTC().Format(time.RFC3339)
	targetURL := url("/" + bucket + "/" + key + "?lock&retain-until=" + retainStr)
	req, _ := http.NewRequest(http.MethodPut, targetURL, nil)
	resp, err := do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := expectStatus(resp, http.StatusOK); err != nil {
		return err
	}
	fmt.Printf("Locked %s/%s until %s\n", bucket, key, retainStr)
	return nil
}

// parseDuration extends Go's time.ParseDuration with d (days) and y (years) suffixes.
func parseDuration(s string) (time.Time, error) {
	now := time.Now()
	if strings.HasSuffix(s, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return time.Time{}, err
		}
		return now.AddDate(0, 0, days), nil
	}
	if strings.HasSuffix(s, "y") {
		years, err := strconv.Atoi(strings.TrimSuffix(s, "y"))
		if err != nil {
			return time.Time{}, err
		}
		return now.AddDate(years, 0, 0), nil
	}
	dur, err := time.ParseDuration(s)
	if err != nil {
		return time.Time{}, err
	}
	return now.Add(dur), nil
}

// ---------- Lifecycle commands ----------

func cmdLCSet(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: lc-set <bucket> <days> [prefix]")
	}
	bucket := args[0]
	days, err := strconv.Atoi(args[1])
	if err != nil || days <= 0 {
		return fmt.Errorf("days must be a positive integer")
	}
	prefix := ""
	if len(args) > 2 {
		prefix = args[2]
	}

	body := fmt.Sprintf(`<LifecycleConfiguration>
  <Rule>
    <ID>cli-rule</ID>
    <Status>Enabled</Status>
    <Filter><Prefix>%s</Prefix></Filter>
    <Expiration><Days>%d</Days></Expiration>
  </Rule>
</LifecycleConfiguration>`, prefix, days)

	req, _ := http.NewRequest(http.MethodPut, url("/"+bucket+"?lifecycle"), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/xml")
	resp, err := do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := expectStatus(resp, http.StatusOK); err != nil {
		return err
	}
	if prefix != "" {
		fmt.Printf("Lifecycle set on '%s': expire objects with prefix '%s' after %d days\n", bucket, prefix, days)
	} else {
		fmt.Printf("Lifecycle set on '%s': expire all objects after %d days\n", bucket, days)
	}
	return nil
}

type lcResult struct {
	Rules []struct {
		ID     string `xml:"ID"`
		Status string `xml:"Status"`
		Filter struct {
			Prefix string `xml:"Prefix"`
		} `xml:"Filter"`
		Expiration struct {
			Days int `xml:"Days"`
		} `xml:"Expiration"`
	} `xml:"Rule"`
}

func cmdLCGet(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: lc-get <bucket>")
	}
	bucket := args[0]
	req, _ := http.NewRequest(http.MethodGet, url("/"+bucket+"?lifecycle"), nil)
	resp, err := do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		fmt.Printf("No lifecycle configuration on '%s'\n", bucket)
		return nil
	}
	if err := expectStatus(resp, http.StatusOK); err != nil {
		return err
	}
	var result lcResult
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tPREFIX\tEXPIRY DAYS")
	for _, rule := range result.Rules {
		prefix := rule.Filter.Prefix
		if prefix == "" {
			prefix = "(all)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\n", rule.ID, rule.Status, prefix, rule.Expiration.Days)
	}
	w.Flush()
	return nil
}

func cmdLCDel(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: lc-del <bucket>")
	}
	bucket := args[0]
	req, _ := http.NewRequest(http.MethodDelete, url("/"+bucket+"?lifecycle"), nil)
	resp, err := do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := expectStatus(resp, http.StatusOK, http.StatusNoContent); err != nil {
		return err
	}
	fmt.Printf("Lifecycle configuration removed from '%s'\n", bucket)
	return nil
}

func cmdPolicySet(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: policy-set <username> <file.json>")
	}
	username := args[0]
	filepath := args[1]

	data, err := os.ReadFile(filepath)
	if err != nil {
		return fmt.Errorf("read policy file: %w", err)
	}

	req, err := http.NewRequest(http.MethodPut, url("/console/users/"+username+"/policy"), strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if err := expectStatus(resp, http.StatusOK, http.StatusNoContent); err != nil {
		return err
	}

	fmt.Printf("Policy successfully attached to user '%s'.\n", username)
	return nil
}

func cmdPolicyGet(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: policy-get <username>")
	}
	username := args[0]

	req, err := http.NewRequest(http.MethodGet, url("/console/users/"+username+"/policy"), nil)
	if err != nil {
		return err
	}

	resp, err := do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if err := expectStatus(resp, http.StatusOK); err != nil {
		return err
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	fmt.Println(string(body))
	return nil
}

func cmdPolicyDel(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: policy-del <username>")
	}
	username := args[0]

	req, err := http.NewRequest(http.MethodDelete, url("/console/users/"+username+"/policy"), nil)
	if err != nil {
		return err
	}

	resp, err := do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if err := expectStatus(resp, http.StatusOK, http.StatusNoContent); err != nil {
		return err
	}

	fmt.Printf("Policy successfully removed from user '%s'.\n", username)
	return nil
}

type nodeInfo struct {
	NodeID   string    `json:"node_id"`
	Address  string    `json:"address"`
	Status   string    `json:"status"`
	LastSeen time.Time `json:"last_seen"`
	Load     int64     `json:"load"`
}

func cmdClusterStatus(args []string) error {
	req, err := http.NewRequest(http.MethodGet, url("/console/cluster/status"), nil)
	if err != nil {
		return err
	}

	resp, err := do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if err := expectStatus(resp, http.StatusOK); err != nil {
		return err
	}

	var nodes []nodeInfo
	if err := json.NewDecoder(resp.Body).Decode(&nodes); err != nil {
		return fmt.Errorf("decode status: %w", err)
	}

	if len(nodes) == 0 {
		fmt.Println("No active clustering detected.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "NODE ID\tADDRESS\tSTATUS\tLAST SEEN\tLOAD")
	for _, node := range nodes {
		seen := "N/A"
		if !node.LastSeen.IsZero() {
			seen = node.LastSeen.Format("15:04:05")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\n", node.NodeID, node.Address, node.Status, seen, node.Load)
	}
	w.Flush()
	return nil
}

func cmdPlacement(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: placement <bucket> <key>")
	}
	bucket, key := args[0], args[1]

	targetURL := url("/console/cluster/placement?bucket=" + bucket + "&key=" + key)
	req, err := http.NewRequest(http.MethodGet, targetURL, nil)
	if err != nil {
		return err
	}

	resp, err := do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if err := expectStatus(resp, http.StatusOK); err != nil {
		return err
	}

	var res struct {
		Bucket  string `json:"bucket"`
		Key     string `json:"key"`
		NodeID  string `json:"node_id"`
		Address string `json:"address"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return fmt.Errorf("decode placement: %w", err)
	}

	fmt.Printf("Bucket:     %s\n", res.Bucket)
	fmt.Printf("Key:        %s\n", res.Key)
	fmt.Printf("Owner Node: %s (%s)\n", res.NodeID, res.Address)
	return nil
}

// ---------- helpers ----------

var client = &http.Client{Timeout: 30 * time.Second}

func url(path string) string {
	return strings.TrimRight(endpoint, "/") + path
}

func do(req *http.Request) (*http.Response, error) {
	if accessKey != "" && secretKey != "" {
		req.SetBasicAuth(accessKey, secretKey)
	}
	return client.Do(req)
}

func expectStatus(resp *http.Response, codes ...int) error {
	for _, c := range codes {
		if resp.StatusCode == c {
			return nil
		}
	}
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
}

func formatDate(s string) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return t.Format("2006-01-02 15:04:05")
}

func fatalf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format, a...)
	os.Exit(1)
}
