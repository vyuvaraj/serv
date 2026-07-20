package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/vyuvaraj/serv/packages/ServTrace/pkg/store"
)

func TestSelfHealingObservabilityLoop(t *testing.T) {
	rollbackTriggered := false
	var rolledBackService string

	// 1. Start Mock ServCloud server
	cloudMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/services/payment-service/rollback" {
			rollbackTriggered = true
			rolledBackService = "payment-service"
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"success"}`))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer cloudMock.Close()

	os.Setenv("SERV_CLOUD_URL", cloudMock.URL)
	defer os.Unsetenv("SERV_CLOUD_URL")

	// 2. Initialize ServTrace Store & Server
	ts := store.NewStore(100)
	s := NewServer(ts)
	_ = s // NewServer automatically starts the loop

	// 3. Add 5 error spans for 'payment-service'
	var spans []store.Span
	for i := 0; i < 6; i++ {
		spans = append(spans, store.Span{
			TraceID:   fmt.Sprintf("trace-%d", i),
			SpanID:    fmt.Sprintf("span-%d", i),
			Name:      "processPayment",
			StartTime: time.Now().UnixNano(),
			EndTime:   time.Now().UnixNano() + 50*1e6,
			Status:    2, // Status 2 = Error
			Service:   "payment-service",
		})
	}
	ts.AddSpans(spans)

	// 4. Wait for the self-healing background check to trigger rollback (up to 3 seconds)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if rollbackTriggered {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !rollbackTriggered {
		t.Fatal("Self-healing did not trigger rollback for payment-service on ServCloud")
	}

	if rolledBackService != "payment-service" {
		t.Errorf("Expected rolled back service to be 'payment-service', got '%s'", rolledBackService)
	}
}
