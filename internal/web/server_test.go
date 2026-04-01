package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	awsutil "github.com/bovinemagnet/gossm/internal/aws"
	"github.com/bovinemagnet/gossm/internal/config"
	"github.com/bovinemagnet/gossm/internal/session"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	sm := session.New(nil, nil)
	cfg := &config.Config{
		DashboardPort: 8877,
		LogLevel:      "warn",
		PIDDir:        t.TempDir(),
	}
	s := NewServer(sm, cfg, time.Now(), nil)
	t.Cleanup(func() { s.Stop() })
	return s
}

func testServerWithPresets(t *testing.T) *Server {
	t.Helper()
	sm := session.New(nil, nil)
	cfg := &config.Config{
		DashboardPort: 8877,
		LogLevel:      "warn",
		PIDDir:        t.TempDir(),
		Presets: []config.SessionPreset{
			{
				Name:         "web-shell",
				InstanceID:   "i-web001",
				InstanceName: "web-server",
				Profile:      "prod",
				SessionType:  "shell",
			},
			{
				Name:         "db-tunnel",
				InstanceID:   "i-db001",
				InstanceName: "db-server",
				Profile:      "prod",
				SessionType:  "port-forward",
				LocalPort:    5432,
				RemotePort:   5432,
				RemoteHost:   "db.internal",
			},
		},
	}
	s := NewServer(sm, cfg, time.Now(), nil)
	t.Cleanup(func() { s.Stop() })
	return s
}

func TestDashboardHandler(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET / status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if len(body) == 0 {
		t.Error("GET / returned empty body")
	}
	// The dashboard HTML should mention "gossm" somewhere.
	if !containsString(body, "gossm") {
		t.Error("GET / body does not contain \"gossm\"")
	}
}

func TestSessionsListHandler(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /api/sessions status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestStopSessionHandler(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodDelete, "/api/sessions/nonexistent-id", nil)
	w := httptest.NewRecorder()

	// Should not panic; will return an error status since the session does not exist.
	srv.Handler().ServeHTTP(w, req)

	// We expect a 500 because StopSession will return "session not found".
	if w.Code != http.StatusInternalServerError {
		t.Errorf("DELETE /api/sessions/nonexistent status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestStaticHandler(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/static/htmx.min.js", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /static/htmx.min.js status = %d, want %d", w.Code, http.StatusOK)
	}
	if w.Body.Len() == 0 {
		t.Error("GET /static/htmx.min.js returned empty body")
	}
}

func TestGetPreset_Valid(t *testing.T) {
	srv := testServerWithPresets(t)
	req := httptest.NewRequest(http.MethodGet, "/api/presets/0", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/presets/0 status = %d, want %d", w.Code, http.StatusOK)
	}

	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var p config.SessionPreset
	if err := json.NewDecoder(w.Body).Decode(&p); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}
	if p.Name != "web-shell" {
		t.Errorf("Name = %q, want %q", p.Name, "web-shell")
	}
	if p.InstanceID != "i-web001" {
		t.Errorf("InstanceID = %q, want %q", p.InstanceID, "i-web001")
	}
}

func TestGetPreset_SecondPreset(t *testing.T) {
	srv := testServerWithPresets(t)
	req := httptest.NewRequest(http.MethodGet, "/api/presets/1", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/presets/1 status = %d, want %d", w.Code, http.StatusOK)
	}

	var p config.SessionPreset
	if err := json.NewDecoder(w.Body).Decode(&p); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}
	if p.Name != "db-tunnel" {
		t.Errorf("Name = %q, want %q", p.Name, "db-tunnel")
	}
	if p.SessionType != "port-forward" {
		t.Errorf("SessionType = %q, want %q", p.SessionType, "port-forward")
	}
	if p.LocalPort != 5432 {
		t.Errorf("LocalPort = %d, want 5432", p.LocalPort)
	}
}

func TestGetPreset_InvalidIndex(t *testing.T) {
	srv := testServerWithPresets(t)

	tests := []struct {
		name string
		path string
	}{
		{"negative", "/api/presets/-1"},
		{"out of range", "/api/presets/99"},
		{"non-numeric", "/api/presets/abc"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("GET %s status = %d, want %d", tc.path, w.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestGetPreset_NoPresets(t *testing.T) {
	srv := testServer(t) // no presets configured
	req := httptest.NewRequest(http.MethodGet, "/api/presets/0", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("GET /api/presets/0 (no presets) status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestStartPreset_InvalidIndex(t *testing.T) {
	srv := testServerWithPresets(t)

	tests := []struct {
		name string
		path string
	}{
		{"negative", "/api/presets/-1/start"},
		{"out of range", "/api/presets/99/start"},
		{"non-numeric", "/api/presets/abc/start"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, nil)
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("POST %s status = %d, want %d", tc.path, w.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestStartPreset_ShellSession(t *testing.T) {
	srv := testServerWithPresets(t)
	defer srv.sm.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/presets/0/start", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/presets/0/start status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	sessions := srv.sm.ListSessions()
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].InstanceID != "i-web001" {
		t.Errorf("InstanceID = %q, want %q", sessions[0].InstanceID, "i-web001")
	}
	if sessions[0].Type != session.TypeShell {
		t.Errorf("Type = %v, want TypeShell", sessions[0].Type)
	}
}

func TestStartPreset_PortForwardSession(t *testing.T) {
	srv := testServerWithPresets(t)
	defer srv.sm.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/presets/1/start", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/presets/1/start status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	sessions := srv.sm.ListSessions()
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	s := sessions[0]
	if s.InstanceID != "i-db001" {
		t.Errorf("InstanceID = %q, want %q", s.InstanceID, "i-db001")
	}
	if s.Type != session.TypePortForward {
		t.Errorf("Type = %v, want TypePortForward", s.Type)
	}
	if s.LocalPort != 5432 {
		t.Errorf("LocalPort = %d, want 5432", s.LocalPort)
	}
	if s.RemotePort != 5432 {
		t.Errorf("RemotePort = %d, want 5432", s.RemotePort)
	}
	if s.RemoteHost != "db.internal" {
		t.Errorf("RemoteHost = %q, want %q", s.RemoteHost, "db.internal")
	}
}

func TestDashboardIncludesPresets(t *testing.T) {
	srv := testServerWithPresets(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want %d", w.Code, http.StatusOK)
	}

	body := w.Body.String()
	if !strings.Contains(body, "web-shell") {
		t.Error("dashboard does not contain preset name \"web-shell\"")
	}
	if !strings.Contains(body, "db-tunnel") {
		t.Error("dashboard does not contain preset name \"db-tunnel\"")
	}
	if !strings.Contains(body, "Saved Sessions") {
		t.Error("dashboard does not contain \"Saved Sessions\" heading")
	}
}

func TestDashboardNoPresetsSection(t *testing.T) {
	srv := testServer(t) // no presets
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want %d", w.Code, http.StatusOK)
	}

	body := w.Body.String()
	if strings.Contains(body, "Saved Sessions") {
		t.Error("dashboard should not contain \"Saved Sessions\" when no presets configured")
	}
}

// --- Mock EC2 API for instance picker tests ---

type mockEC2API struct {
	output *ec2.DescribeInstancesOutput
	err    error
}

func (m *mockEC2API) DescribeInstances(_ context.Context, _ *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return m.output, m.err
}

func mockFactory(api awsutil.EC2DescribeInstancesAPI, err error) EC2ClientFactory {
	return func(_ context.Context, _ string) (awsutil.EC2DescribeInstancesAPI, error) {
		return api, err
	}
}

func testServerWithEC2(t *testing.T, factory EC2ClientFactory) *Server {
	t.Helper()
	sm := session.New(nil, nil)
	cfg := &config.Config{
		DashboardPort: 8877,
		LogLevel:      "warn",
		PIDDir:        t.TempDir(),
	}
	s := NewServer(sm, cfg, time.Now(), factory)
	t.Cleanup(func() { s.Stop() })
	return s
}

// --- Instance picker handler tests ---

func TestHandleInstances_NoProfile(t *testing.T) {
	srv := testServerWithEC2(t, mockFactory(&mockEC2API{}, nil))
	req := httptest.NewRequest(http.MethodGet, "/api/instances", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), "Enter an AWS profile") {
		t.Error("expected profile prompt message in response")
	}
}

func TestHandleInstances_NilFactory(t *testing.T) {
	srv := testServerWithEC2(t, nil) // nil factory
	req := httptest.NewRequest(http.MethodGet, "/api/instances?profile=prod", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleInstances_Success(t *testing.T) {
	api := &mockEC2API{
		output: &ec2.DescribeInstancesOutput{
			Reservations: []types.Reservation{
				{
					Instances: []types.Instance{
						{
							InstanceId:       aws.String("i-aaa111"),
							Tags:             []types.Tag{{Key: aws.String("Name"), Value: aws.String("web-server")}},
							State:            &types.InstanceState{Name: types.InstanceStateNameRunning},
							InstanceType:     types.InstanceTypeT3Micro,
							PrivateIpAddress: aws.String("10.0.0.1"),
							Placement:        &types.Placement{AvailabilityZone: aws.String("eu-west-1a")},
						},
						{
							InstanceId:       aws.String("i-bbb222"),
							Tags:             []types.Tag{{Key: aws.String("Name"), Value: aws.String("api-server")}},
							State:            &types.InstanceState{Name: types.InstanceStateNameRunning},
							InstanceType:     types.InstanceTypeT3Small,
							PrivateIpAddress: aws.String("10.0.0.2"),
							Placement:        &types.Placement{AvailabilityZone: aws.String("eu-west-1b")},
						},
					},
				},
			},
		},
	}
	srv := testServerWithEC2(t, mockFactory(api, nil))
	req := httptest.NewRequest(http.MethodGet, "/api/instances?profile=prod", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "i-aaa111") {
		t.Error("response does not contain instance ID i-aaa111")
	}
	if !strings.Contains(body, "web-server") {
		t.Error("response does not contain instance name web-server")
	}
	if !strings.Contains(body, "i-bbb222") {
		t.Error("response does not contain instance ID i-bbb222")
	}
	if !strings.Contains(body, "api-server") {
		t.Error("response does not contain instance name api-server")
	}
	if !strings.Contains(body, "10.0.0.1") {
		t.Error("response does not contain private IP 10.0.0.1")
	}
}

func TestHandleInstances_EmptyResults(t *testing.T) {
	api := &mockEC2API{
		output: &ec2.DescribeInstancesOutput{},
	}
	srv := testServerWithEC2(t, mockFactory(api, nil))
	req := httptest.NewRequest(http.MethodGet, "/api/instances?profile=dev", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), "No running instances") {
		t.Error("expected 'No running instances' message for empty results")
	}
}

func TestHandleInstances_APIError(t *testing.T) {
	api := &mockEC2API{
		err: fmt.Errorf("access denied"),
	}
	srv := testServerWithEC2(t, func(_ context.Context, _ string) (awsutil.EC2DescribeInstancesAPI, error) {
		return api, nil
	})
	req := httptest.NewRequest(http.MethodGet, "/api/instances?profile=prod", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if !strings.Contains(w.Body.String(), "access denied") {
		t.Error("expected error message to contain 'access denied'")
	}
}

func TestHandleInstances_FactoryError(t *testing.T) {
	factory := func(_ context.Context, _ string) (awsutil.EC2DescribeInstancesAPI, error) {
		return nil, fmt.Errorf("invalid profile")
	}
	srv := testServerWithEC2(t, factory)
	req := httptest.NewRequest(http.MethodGet, "/api/instances?profile=bad", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if !strings.Contains(w.Body.String(), "invalid profile") {
		t.Error("expected error message to contain 'invalid profile'")
	}
}

func TestHandleInstances_WithFilter(t *testing.T) {
	api := &mockEC2API{
		output: &ec2.DescribeInstancesOutput{
			Reservations: []types.Reservation{
				{
					Instances: []types.Instance{
						{
							InstanceId: aws.String("i-filtered"),
							Tags:       []types.Tag{{Key: aws.String("Name"), Value: aws.String("filtered-instance")}},
							State:      &types.InstanceState{Name: types.InstanceStateNameRunning},
						},
					},
				},
			},
		},
	}
	srv := testServerWithEC2(t, mockFactory(api, nil))
	req := httptest.NewRequest(http.MethodGet, "/api/instances?profile=prod&filter=web", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), "i-filtered") {
		t.Error("response does not contain filtered instance")
	}
}

func TestDashboardIncludesInstancePicker(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, "instance-picker") {
		t.Error("dashboard does not contain instance-picker container")
	}
	if !strings.Contains(body, "/api/instances") {
		t.Error("dashboard does not contain /api/instances HTMX endpoint")
	}
}

// containsString is a small helper to check substring presence.
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && stringContains(s, substr))
}

func stringContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
