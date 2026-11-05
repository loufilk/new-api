package main

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"time"
)

const ProtocolVersion = 1
const MaxRequestBytes = 256 * 1024
const MaxResultBytes = 512 * 1024

const requestTimeout = 15 * time.Second

type ReadyResponse struct {
	ProtocolVersion int    `json:"protocol_version"`
	SDKCommit       string `json:"sdk_commit"`
	DataVersion     string `json:"data_version"`
	DataSHA256      string `json:"data_sha256"`
}

type AnalyzeRequest struct {
	Samples []Sample `json:"samples"`
	Strict  bool     `json:"strict"`
}

type service struct {
	engine *Engine
	slots  chan struct{}
}

func newServer(engine *Engine) *http.Server {
	service := &service{engine: engine, slots: make(chan struct{}, 4)}
	return &http.Server{
		Handler:           service,
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      requestTimeout,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    8 * 1024,
		// net/http otherwise logs panic details and request paths. This service
		// deliberately emits no request logs or raw SDK error messages.
		ErrorLog: log.New(io.Discard, "", 0),
	}
}

func (s *service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if recover() != nil {
			writeError(w, http.StatusInternalServerError, "fingerprint service unavailable")
		}
	}()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if s.engine == nil || s.engine.detector == nil {
		writeError(w, http.StatusServiceUnavailable, "fingerprint service unavailable")
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "invalid fingerprint request")
		return
	}
	switch r.URL.Path {
	case "/v1/ready", "/v1/challenges":
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if r.ContentLength != 0 {
			writeError(w, http.StatusBadRequest, "invalid fingerprint request")
			return
		}
		if r.URL.Path == "/v1/ready" {
			writeJSON(w, http.StatusOK, ReadyResponse{ProtocolVersion: ProtocolVersion, SDKCommit: SDKCommit,
				DataVersion: s.engine.version, DataSHA256: referenceDetectorSHA256})
			return
		}
		challenges, err := GenerateChallenges()
		if err != nil || len(challenges) != 3 {
			writeError(w, http.StatusInternalServerError, "fingerprint challenge generation failed")
			return
		}
		writeJSON(w, http.StatusOK, struct {
			Challenges any `json:"challenges"`
		}{Challenges: challenges})
	case "/v1/analyze":
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" || r.Header.Get("Content-Encoding") != "" {
			writeError(w, http.StatusUnsupportedMediaType, "application/json required")
			return
		}
		if r.ContentLength > MaxRequestBytes {
			writeError(w, http.StatusRequestEntityTooLarge, "fingerprint request too large")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, MaxRequestBytes)
		defer r.Body.Close()
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var request AnalyzeRequest
		if err := decoder.Decode(&request); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeError(w, http.StatusRequestEntityTooLarge, "fingerprint request too large")
			} else {
				writeError(w, http.StatusBadRequest, "invalid fingerprint request")
			}
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeError(w, http.StatusRequestEntityTooLarge, "fingerprint request too large")
			} else {
				writeError(w, http.StatusBadRequest, "invalid fingerprint request")
			}
			return
		}
		if len(request.Samples) != 3 {
			writeError(w, http.StatusBadRequest, "invalid fingerprint request")
			return
		}
		for _, sample := range request.Samples {
			if len(sample.Response) > MaxResponseBytes || len(sample.Error) > 128 {
				writeError(w, http.StatusBadRequest, "invalid fingerprint request")
				return
			}
		}
		select {
		case s.slots <- struct{}{}:
			defer func() { <-s.slots }()
		default:
			writeError(w, http.StatusServiceUnavailable, "fingerprint service busy")
			return
		}
		if r.Context().Err() != nil {
			return
		}
		result, err := s.engine.Analyze(request.Samples, request.Strict)
		if err != nil {
			status := http.StatusInternalServerError
			message := "fingerprint analysis failed"
			if errors.Is(err, errInvalidSamples) {
				status, message = http.StatusBadRequest, "invalid fingerprint request"
			}
			writeError(w, status, message)
			return
		}
		writeJSON(w, http.StatusOK, result)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, struct {
		Error string `json:"error"`
	}{Error: message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil || len(data) > MaxResultBytes {
		status = http.StatusInternalServerError
		data = []byte(`{"error":"fingerprint response unavailable"}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}
