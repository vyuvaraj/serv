package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestPhase35UtilityNamespacesPart6(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test_utility_namespaces_part6_*.srv")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	srvContent := `
	test "verify phase 35 utility namespaces part 6" {
		// 1. Format Namespace
		assert format.bytes(1048576) == "1 MB"
		assert format.bytes(1572864) == "1.50 MB"
		assert format.number(1500000) == "1.5M"
		assert format.number(999) == "999"
		assert format.percent(0.856) == "85.6%"
		assert format.plural(1, "item", "items") == "1 item"
		assert format.plural(5, "item", "items") == "5 items"

		// 2. IP Namespace
		let parsed = ip.parse("192.168.1.1")
		assert parsed.version == 4.0
		assert parsed.octets.length() == 4
		assert parsed.octets[0] == 192.0

		assert ip.isPrivate("192.168.1.1") == true
		assert ip.isPrivate("8.8.8.8") == false

		assert ip.inCIDR("10.0.0.5", "10.0.0.0/8") == true
		assert ip.inCIDR("192.168.1.1", "10.0.0.0/8") == false

		assert ip.version("2001:db8::68") == "ipv6"
		assert ip.version("1.1.1.1") == "ipv4"

		// 3. DNS Namespace (Test Mocks)
		assert dns.lookup("test.local") == "127.0.0.1"
		assert dns.txt("test.local") == "v=spf1 -all"
		let srvInfo = dns.srv("test.local")
		assert srvInfo.host == "app.test.local"
		assert srvInfo.port == 8080.0
		assert srvInfo.priority == 10.0

		// 4. Multipart Namespace
		// Craft a raw multipart body
		let multipartBody = "--boundary123\r\nContent-Disposition: form-data; name=\"username\"\r\n\r\nalice\r\n--boundary123\r\nContent-Disposition: form-data; name=\"avatar\"; filename=\"avatar.png\"\r\nContent-Type: image/png\r\n\r\nfake-png-content\r\n--boundary123--\r\n"
		let req = {
			"method": "POST",
			"path": "/upload",
			"body": multipartBody,
			"headers": {
				"content-type": "multipart/form-data; boundary=boundary123"
			},
			"params": {},
			"query": {}
		}

		let parsedForm = multipart.parse(req)
		assert parsedForm.fields.username == "alice"
		assert parsedForm.files.length() == 1
		assert parsedForm.files[0].filename == "avatar.png"
		assert parsedForm.files[0].content == "fake-png-content"
	}
	`
	if _, err := tmpFile.WriteString(srvContent); err != nil {
		t.Fatalf("failed to write srv file: %v", err)
	}
	tmpFile.Close()

	cmd := exec.Command("go", "run", ".", "test", tmpFile.Name())
	cmd.Env = append(os.Environ(), "TESTING=true", "SERV_ENV=test")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	output := stdout.String() + "\n" + stderr.String()
	t.Logf("Compiler Exec Output:\n%s", output)

	if err != nil {
		t.Fatalf("Compiler test execution failed: %v\nError output:\n%s", err, output)
	}

	if !strings.Contains(output, "PASS") {
		t.Error("Expected test execution to pass")
	}
}
