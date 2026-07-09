package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestEventSourcingDSL(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test_event_sourcing_*.srv")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	srvContent := `
	event_store "orders" {
		command PlaceOrder(orderId, amount) {
			emit "OrderPlaced" { "orderId": orderId, "amount": amount }
		}

		on "OrderPlaced" (event) {
			log.info("Received event: " + event)
		}
	}

	test "verify event sourcing block compiles" {
		assert 1 == 1
	}
	`
	if _, err := tmpFile.WriteString(srvContent); err != nil {
		t.Fatalf("failed to write srv file: %v", err)
	}
	tmpFile.Close()

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	done := make(chan string)
	go func() {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r)
		done <- buf.String()
	}()

	runTests(tmpFile.Name(), false, "")

	w.Close()
	os.Stdout = oldStdout
	output := <-done

	t.Logf("Compiler Exec Output:\n%s", output)

	if !strings.Contains(output, "PASS") {
		t.Error("Expected tests to pass")
	}
	if strings.Contains(output, "FAIL") {
		t.Error("Expected no tests to fail")
	}
}
