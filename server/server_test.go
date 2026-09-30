package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"axiombox/executor"

	"github.com/gorilla/websocket"
)

func TestHealthEndpoint(t *testing.T) {
	srv := NewServer(2, 10)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/health")
	if err != nil {
		t.Fatalf("Health check failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected 200 OK, got %d", resp.StatusCode)
	}

	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("Failed to decode health json: %v", err)
	}

	if data["status"] != "HEALTHY" {
		t.Errorf("Expected status HEALTHY, got %v", data["status"])
	}
}

func TestExecuteEndpoint(t *testing.T) {
	srv := NewServer(2, 10)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	reqPayload := executor.PipelineRequest{
		Language: "python",
		Code:     `print("SERVER_TEST_OK")`,
	}
	body, _ := json.Marshal(reqPayload)

	resp, err := http.Post(ts.URL+"/api/execute", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Execute request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", resp.StatusCode)
	}

	var res executor.PipelineResult
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if res.Status != "COMPLETED" {
		t.Errorf("Expected COMPLETED, got %s", res.Status)
	}
	if !strings.Contains(res.Stdout, "SERVER_TEST_OK") {
		t.Errorf("Expected stdout to contain SERVER_TEST_OK, got: %s", res.Stdout)
	}
}

func TestRateLimiter(t *testing.T) {
	rl := NewRateLimiter(3, 500*time.Millisecond)

	ip := "192.168.1.100"
	if !rl.Allow(ip) {
		t.Error("Request 1 should be allowed")
	}
	if !rl.Allow(ip) {
		t.Error("Request 2 should be allowed")
	}
	if !rl.Allow(ip) {
		t.Error("Request 3 should be allowed")
	}
	if rl.Allow(ip) {
		t.Error("Request 4 should have been blocked by rate limiter")
	}

	// Sleep until window expires
	time.Sleep(550 * time.Millisecond)
	if !rl.Allow(ip) {
		t.Error("Request 5 after window reset should be allowed")
	}
}

func TestWebSocketTelemetryStream(t *testing.T) {
	srv := NewServer(2, 10)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Convert http URL to ws URL
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/ws/execute"

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket connection failed: %v", err)
	}
	defer conn.Close()

	req := executor.PipelineRequest{
		Language: "javascript",
		Code:     `console.log("WS_STREAM_OK");`,
	}
	if err := conn.WriteJSON(req); err != nil {
		t.Fatalf("Failed to send WebSocket payload: %v", err)
	}

	var receivedStdout, receivedFinished bool
	for {
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		var msg wsMessage
		if err := conn.ReadJSON(&msg); err != nil {
			break
		}

		if msg.Type == "stdout" {
			receivedStdout = true
		}
		if msg.Type == "finished" {
			receivedFinished = true
			break
		}
	}

	if !receivedStdout {
		t.Errorf("Did not receive stdout message over WebSocket")
	}
	if !receivedFinished {
		t.Errorf("Did not receive finished message over WebSocket")
	}
}
