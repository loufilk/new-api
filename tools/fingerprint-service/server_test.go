package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	lmfpd "github.com/Ikaleio/lm-detector/sdk/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPReadinessAndChallenges(t *testing.T) {
	handler := newServer(referenceEngine(t)).Handler
	ready := httptest.NewRecorder()
	handler.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/v1/ready", nil))
	require.Equal(t, http.StatusOK, ready.Code)
	var metadata ReadyResponse
	require.NoError(t, json.Unmarshal(ready.Body.Bytes(), &metadata))
	assert.Equal(t, ProtocolVersion, metadata.ProtocolVersion)
	assert.Equal(t, SDKCommit, metadata.SDKCommit)
	assert.Equal(t, referenceDetectorSHA256, metadata.DataSHA256)
	assert.NotEmpty(t, metadata.DataVersion)
	assert.Equal(t, "no-store", ready.Header().Get("Cache-Control"))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/challenges", nil))
	require.Equal(t, http.StatusOK, response.Code)
	var challenges struct {
		Challenges []lmfpd.Challenge `json:"challenges"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &challenges))
	require.Len(t, challenges.Challenges, 3)
	for _, challenge := range challenges.Challenges {
		assert.True(t, validChallengeID(challenge.ID))
		assert.NotEmpty(t, challenge.Prompt)
		assert.GreaterOrEqual(t, challenge.ExpectedCount, 292)
		assert.LessOrEqual(t, challenge.ExpectedCount, 332)
	}

	unavailable := httptest.NewRecorder()
	newServer(nil).Handler.ServeHTTP(unavailable, httptest.NewRequest(http.MethodGet, "/v1/ready", nil))
	assert.Equal(t, http.StatusServiceUnavailable, unavailable.Code)
	assert.JSONEq(t, `{"error":"fingerprint service unavailable"}`, unavailable.Body.String())
}

func TestHTTPScoringContract(t *testing.T) {
	handler := newServer(referenceEngine(t)).Handler
	for _, strict := range []bool{true, false} {
		samples := referenceSamples(t)
		if !strict {
			samples[2].Error = "request_failed"
			samples[2].Response = ""
		}
		body, err := json.Marshal(AnalyzeRequest{Samples: samples, Strict: strict})
		require.NoError(t, err)
		request := httptest.NewRequest(http.MethodPost, "/v1/analyze", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json; charset=utf-8")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.LessOrEqual(t, response.Body.Len(), MaxResultBytes)
		var result Result
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
		if strict {
			assert.Equal(t, "scored", result.Status)
			assert.Equal(t, "claude-haiku-4.5", result.TopCandidates[0].Model)
			assert.Equal(t, "reference_calibrated", result.ProbabilityStatus)
		} else {
			assert.Equal(t, "partial", result.Status)
			assert.Equal(t, "unavailable", result.ProbabilityStatus)
			for _, candidate := range result.TopCandidates {
				assert.Nil(t, candidate.Probability)
			}
		}
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &fields))
		keys := make([]string, 0, len(fields))
		for key := range fields {
			keys = append(keys, key)
		}
		assert.ElementsMatch(t, []string{"status", "reason", "top_candidates", "sample_quality", "data_version", "data_sha256", "sdk_commit", "probability_note", "probability_status", "used_outputs", "strict"}, keys)
	}
}

func TestHTTPRejectsInvalidInputsWithoutEchoing(t *testing.T) {
	handler := newServer(referenceEngine(t)).Handler
	valid, err := json.Marshal(AnalyzeRequest{Samples: referenceSamples(t), Strict: true})
	require.NoError(t, err)
	longSample := referenceSamples(t)
	longSample[0].Response = strings.Repeat("1", MaxResponseBytes+1)
	oversizeSample, err := json.Marshal(AnalyzeRequest{Samples: longSample, Strict: true})
	require.NoError(t, err)
	longSample[0].Response = ""
	longSample[0].Error = strings.Repeat("x", 129)
	oversizeError, err := json.Marshal(AnalyzeRequest{Samples: longSample, Strict: true})
	require.NoError(t, err)
	for _, test := range []struct {
		name        string
		method      string
		path        string
		contentType string
		encoding    string
		body        string
		chunked     bool
		status      int
	}{
		{name: "unknown top level", body: `{"api_key":"synthetic-secret-must-not-escape"}`, status: http.StatusBadRequest},
		{name: "unknown nested field", body: `{"samples":[{"headers":{"Authorization":"synthetic-secret-must-not-escape"}}]}`, status: http.StatusBadRequest},
		{name: "trailing json", body: string(valid) + ` {"extra":"synthetic-secret-must-not-escape"}`, status: http.StatusBadRequest},
		{name: "malformed", body: `{"samples":`, status: http.StatusBadRequest},
		{name: "empty", status: http.StatusBadRequest},
		{name: "null", body: "null", status: http.StatusBadRequest},
		{name: "wrong type", body: `{"samples":{},"strict":true}`, status: http.StatusBadRequest},
		{name: "fractional expected", body: `{"samples":[{"expected_count":292.1}]}`, status: http.StatusBadRequest},
		{name: "few samples", body: `{"samples":[],"strict":true}`, status: http.StatusBadRequest},
		{name: "oversize sample", body: string(oversizeSample), status: http.StatusBadRequest},
		{name: "oversize error", body: string(oversizeError), status: http.StatusBadRequest},
		{name: "oversize body", body: strings.Repeat(" ", MaxRequestBytes+1), status: http.StatusRequestEntityTooLarge},
		{name: "chunked oversize body", body: strings.Repeat(" ", MaxRequestBytes+1), chunked: true, status: http.StatusRequestEntityTooLarge},
		{name: "chunked trailing oversized whitespace", body: string(valid) + strings.Repeat(" ", MaxRequestBytes), chunked: true, status: http.StatusRequestEntityTooLarge},
		{name: "content type", contentType: "text/plain", body: string(valid), status: http.StatusUnsupportedMediaType},
		{name: "encoded body", encoding: "gzip", body: string(valid), status: http.StatusUnsupportedMediaType},
		{name: "query", path: "/v1/analyze?secret=synthetic-secret-must-not-escape", body: string(valid), status: http.StatusBadRequest},
		{name: "wrong method", method: http.MethodGet, body: string(valid), status: http.StatusMethodNotAllowed},
		{name: "ready wrong method", path: "/v1/ready", body: string(valid), status: http.StatusMethodNotAllowed},
		{name: "challenges wrong method", path: "/v1/challenges", body: string(valid), status: http.StatusMethodNotAllowed},
		{name: "ready body", path: "/v1/ready", method: http.MethodGet, body: "secret", status: http.StatusBadRequest},
		{name: "unknown endpoint", path: "/v1/detect", body: string(valid), status: http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			method, path, contentType := test.method, test.path, test.contentType
			if method == "" {
				method = http.MethodPost
			}
			if path == "" {
				path = "/v1/analyze"
			}
			if contentType == "" {
				contentType = "application/json"
			}
			request := httptest.NewRequest(method, path, strings.NewReader(test.body))
			request.Header.Set("Content-Type", contentType)
			request.Header.Set("Content-Encoding", test.encoding)
			if test.chunked {
				request.ContentLength = -1
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			assert.Equal(t, test.status, response.Code, response.Body.String())
			assert.NotContains(t, response.Body.String(), "synthetic-secret")
			var message map[string]string
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &message))
			require.Len(t, message, 1)
			assert.NotEmpty(t, message["error"])
		})
	}
}

func TestHTTPBusyAndSDKFailureAreGeneric(t *testing.T) {
	handler := newServer(referenceEngine(t)).Handler.(*service)
	for range cap(handler.slots) {
		handler.slots <- struct{}{}
	}
	body, err := json.Marshal(AnalyzeRequest{Samples: referenceSamples(t), Strict: true})
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "/v1/analyze", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
	assert.JSONEq(t, `{"error":"fingerprint service busy"}`, response.Body.String())

	broken := newServer(&Engine{detector: &lmfpd.Detector{}, version: "invalid"}).Handler
	request = httptest.NewRequest(http.MethodPost, "/v1/analyze", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	broken.ServeHTTP(response, request)
	assert.Equal(t, http.StatusInternalServerError, response.Code)
	assert.JSONEq(t, `{"error":"fingerprint analysis failed"}`, response.Body.String())
}

func socketDirectory(t *testing.T) string {
	t.Helper()
	// Unix socket path length is limited. Keep test paths short and private.
	directory, err := os.MkdirTemp("", "fpd-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return directory
}

func TestUnixSocketProtocolAndPermissions(t *testing.T) {
	socket := filepath.Join(socketDirectory(t), "fp.sock")
	listener, cleanup, err := listenUnix(socket)
	require.NoError(t, err)
	t.Cleanup(cleanup)
	info, err := os.Lstat(socket)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	assert.NotZero(t, info.Mode()&os.ModeSocket)
	server := newServer(referenceEngine(t))
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	t.Cleanup(func() {
		require.NoError(t, server.Close())
		require.ErrorIs(t, <-finished, http.ErrServerClosed)
	})
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	response, err := client.Get("http://fingerprint/v1/ready")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, response.StatusCode)
	require.NoError(t, response.Body.Close())
	body, err := json.Marshal(AnalyzeRequest{Samples: referenceSamples(t), Strict: true})
	require.NoError(t, err)
	response, err = client.Post("http://fingerprint/v1/analyze", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	data, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	var result Result
	require.NoError(t, json.Unmarshal(data, &result))
	assert.Equal(t, "scored", result.Status)
	assert.Equal(t, "claude-haiku-4.5", result.TopCandidates[0].Model)
}

func TestSocketNeverDeletesExistingPaths(t *testing.T) {
	for _, kind := range []string{"file", "directory", "symlink", "socket"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(socketDirectory(t), "fp.sock")
			switch kind {
			case "file":
				require.NoError(t, os.WriteFile(path, []byte("keep me"), 0o600))
			case "directory":
				require.NoError(t, os.Mkdir(path, 0o700))
			case "symlink":
				require.NoError(t, os.Symlink("missing", path))
			case "socket":
				_, cleanup, err := listenUnix(path)
				require.NoError(t, err)
				t.Cleanup(cleanup)
			}
			before, err := os.Lstat(path)
			require.NoError(t, err)
			_, _, err = listenUnix(path)
			require.Error(t, err)
			after, err := os.Lstat(path)
			require.NoError(t, err)
			assert.True(t, os.SameFile(before, after))
		})
	}
}

func TestSocketCleanupPreservesReplacementAndRequiresPrivateDirectory(t *testing.T) {
	directory := socketDirectory(t)
	path := filepath.Join(directory, "fp.sock")
	_, cleanup, err := listenUnix(path)
	require.NoError(t, err)
	require.NoError(t, os.Remove(path))
	require.NoError(t, os.WriteFile(path, []byte("replacement"), 0o600))
	cleanup()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "replacement", string(data))
	require.NoError(t, os.Chmod(directory, 0o755))
	_, _, err = listenUnix(filepath.Join(directory, "public.sock"))
	require.Error(t, err)
	_, _, err = listenUnix("relative.sock")
	require.Error(t, err)
}
