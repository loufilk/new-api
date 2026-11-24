//go:build lane_billing_integration_dev

package relay

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

const (
	tsHead  = "data: " + `{"id":"a","object":"chat.completion.chunk","created":17,"model":"fixture",`
	tsDelta = tsHead + `"choices":[{"index":0,"delta":{"content":"x"},"finish_reason":null}],"usage":null}` + "\n\n"
	tsStop  = tsHead + `"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n"
	tsRaw   = `{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9}`
	tsUsage = tsHead + `"choices":[],"usage":` + tsRaw + "}\n\n"
	tsDone  = "data: [DONE]\n\n"
)

type laneTerminalCloseFailure struct{ io.ReadCloser }

func (laneTerminalCloseFailure) Close() error { return errors.New("synthetic close error") }

func TestLaneNativeTerminalExactWire(t *testing.T) {
	for _, eol := range []string{"\n", "\r\n"} {
		// The old implementation rejected/buffered any whole body over 64KiB.
		wire := strings.ReplaceAll(strings.Repeat(tsDelta, 600)+tsStop+tsUsage+tsDone, "\n", eol)
		var verified int
		src := io.NopCloser(iotest.OneByteReader(strings.NewReader(wire)))
		b := newLaneNativeTerminalBody(src, "fixture", func(raw []byte) bool { verified++; return string(raw) == tsRaw })
		got, err := io.ReadAll(b)
		if err != nil || !bytes.Equal(got, []byte(wire)) || verified != 1 {
			t.Fatalf("wire=%d read=%d verified=%d err=%v", len(wire), len(got), verified, err)
		}
		if _, err := b.Read(make([]byte, 7)); err != io.EOF || verified != 1 {
			t.Fatal("re-read revalidated or ceased to be EOF")
		}
	}
}

func TestLaneNativeTerminalRejectsAmbiguous(t *testing.T) {
	bad := map[string]string{
		"EOF after content":        tsDelta,
		"mid usage":                tsDelta + tsStop + strings.TrimSuffix(tsUsage, "}\n\n"),
		"no DONE":                  tsDelta + tsStop + tsUsage,
		"no finish":                tsDelta + tsUsage + tsDone,
		"duplicate finish":         tsDelta + tsStop + tsStop + tsUsage + tsDone,
		"duplicate usage":          tsDelta + tsStop + tsUsage + tsUsage + tsDone,
		"duplicate DONE":           tsDelta + tsStop + tsUsage + tsDone + tsDone,
		"incomplete event":         tsDelta + tsStop + tsUsage + strings.TrimSuffix(tsDone, "\n"),
		"trailing byte":            tsDelta + tsStop + tsUsage + tsDone + "x",
		"wrong model":              tsDelta + tsStop + strings.Replace(tsUsage, "fixture", "other", 1) + tsDone,
		"wrong id":                 tsDelta + strings.Replace(tsStop, `"id":"a"`, `"id":"b"`, 1) + tsUsage + tsDone,
		"wrong timestamp":          tsDelta + tsStop + strings.Replace(tsUsage, "17", "18", 1) + tsDone,
		"unexpected earlier usage": strings.Replace(tsDelta, `"usage":null`, `"usage":`+tsRaw, 1) + tsStop + tsUsage + tsDone,
		"case alias":               tsDelta + tsStop + strings.Replace(tsUsage, `"usage":`, `"Usage":null,"usage":`, 1) + tsDone,
		"duplicate field":          tsDelta + tsStop + strings.Replace(tsUsage, `"choices":`, `"choices":[],"choices":`, 1) + tsDone,
		"escaped field":            tsDelta + tsStop + strings.Replace(tsUsage, `"usage":`, `"u\u0073age":`, 1) + tsDone,
		"err object":               tsDelta + tsStop + strings.Replace(tsUsage, `"usage":`, `"error":{},"usage":`, 1) + tsDone,
		"overlong line":            strings.Replace(tsDelta, `"x"`, `"`+strings.Repeat("x", laneTerminalLineLimit)+`"`, 1),
	}
	for name, wire := range bad {
		t.Run(name, func(t *testing.T) {
			verified := false
			b := newLaneNativeTerminalBody(io.NopCloser(strings.NewReader(wire)), "fixture", func([]byte) bool { verified = true; return true })
			got, err := io.ReadAll(b)
			if !errors.Is(err, errLaneTerminalStream) || verified || bytes.Contains(got, []byte(`"finish_reason":"stop"`)) || bytes.Contains(got, []byte(tsDone)) {
				t.Fatalf("len=%d verified=%t err=%v", len(got), verified, err)
			}
		})
	}
	for _, name := range []string{"failed verifier", "source close fails", "source read fails"} {
		t.Run(name, func(t *testing.T) {
			wire := tsDelta + tsStop + tsUsage + tsDone
			var src io.ReadCloser = io.NopCloser(strings.NewReader(wire))
			if name == "source close fails" {
				src = laneTerminalCloseFailure{src}
			}
			if name == "source read fails" {
				src = io.NopCloser(io.MultiReader(strings.NewReader(wire), iotest.ErrReader(io.ErrUnexpectedEOF)))
			}
			runs := 0
			b := newLaneNativeTerminalBody(src, "fixture", func([]byte) bool { runs++; return false })
			got, err := io.ReadAll(b)
			if !errors.Is(err, errLaneTerminalStream) || string(got) != tsDelta || (runs == 1) != (name == "failed verifier") {
				t.Fatal(name, len(got), runs, err)
			}
		})
	}
}

func TestLaneNativeTerminalCloseUnblocksTail(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	b := newLaneNativeTerminalBody(r, "fixture", func([]byte) bool { t.Error("aborted body verified"); return false })
	done := make(chan error, 1)
	go func() { _, err := io.Copy(io.Discard, b); done <- err }()
	// Withhold EOF after an otherwise perfect DONE, then cancel. A process
	// that simply trusts the scanner's DONE would have already accepted it.
	if _, err := io.WriteString(w, tsDelta+tsStop+tsUsage+tsDone); err != nil {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("close authorized terminal")
		}
	case <-time.After(time.Second):
		t.Fatal("close failed to unblock upstream read")
	}
}
