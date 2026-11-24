//go:build lane_billing_integration_dev

package relay

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strconv"
	"sync"
	"unicode/utf8"

	"github.com/tidwall/gjson"
)

const laneTerminalLineLimit = 16 << 10

var errLaneTerminalStream = errors.New("native Chat terminal evidence is unconfirmed")

// A deliberately narrow, uninstalled native Chat text profile. Every ordinary
// content event is returned without reading the next event. Only the terminal
// sequence is held: otherwise a native scanner can stop at DONE or treat an
// early finish_reason as completion before the raw evidence is conclusive.
// The existing stream timeout/cancellation owns Close and interrupts reads.
type laneNativeTerminalBody struct {
	source         io.ReadCloser
	reader         *bufio.Reader
	model, id      string
	created        int64
	sawEvent       bool
	sawContent     bool
	frame, pending []byte
	end            error
	verify         func([]byte) bool

	closeOnce sync.Once
	closeErr  error
	mu        sync.Mutex // never held across upstream I/O
	cancelled bool
}

func newLaneNativeTerminalBody(source io.ReadCloser, model string, verify func([]byte) bool) *laneNativeTerminalBody {
	return &laneNativeTerminalBody{source: source, reader: bufio.NewReaderSize(source, laneTerminalLineLimit), model: model, verify: verify}
}

func (b *laneNativeTerminalBody) closeSource() error {
	b.closeOnce.Do(func() { b.closeErr = b.source.Close() })
	return b.closeErr
}

func (b *laneNativeTerminalBody) Close() error {
	b.mu.Lock()
	b.cancelled = true
	b.mu.Unlock()
	return b.closeSource()
}

func (b *laneNativeTerminalBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(b.pending) == 0 && b.end == nil {
		b.pending, b.end = b.next()
	}
	n := copy(p, b.pending)
	b.pending = b.pending[n:]
	if n != 0 {
		return n, nil
	}
	return 0, b.end
}

// Accept one complete single-data-line SSE record, LF or CRLF. Unsupported
// shapes fail closed on the owned path. No output or provider data is logged.
func (b *laneNativeTerminalBody) event() ([]byte, error) {
	line, err := b.reader.ReadSlice('\n')
	if err != nil || len(line) > laneTerminalLineLimit {
		return nil, errLaneTerminalStream
	}
	b.frame = append(b.frame[:0], line...)
	end, err := b.reader.ReadSlice('\n')
	if err != nil || !(bytes.Equal(end, []byte("\n")) || bytes.Equal(end, []byte("\r\n"))) {
		return nil, errLaneTerminalStream
	}
	b.frame = append(b.frame, end...)
	data, ok := bytes.CutPrefix(bytes.TrimSuffix(bytes.TrimSuffix(b.frame[:len(line)], []byte("\n")), []byte("\r")), []byte("data: "))
	if !ok || !utf8.Valid(data) {
		return nil, errLaneTerminalStream
	}
	return data, nil
}

type laneTerminalKind int

const (
	laneInvalid laneTerminalKind = iota
	laneContent
	laneFinish
	laneUsage
)

// Unlike struct decoding, reject duplicate, unknown, or case-aliased fields at
// each envelope level. VerifyTerminalUsage separately checks all priced fields.
func laneTerminalObject(node gjson.Result, keys ...string) bool {
	if !node.IsObject() {
		return false
	}
	var seen uint32
	valid := true
	node.ForEach(func(key, _ gjson.Result) bool {
		for i, k := range keys {
			if key.Raw == `"`+k+`"` {
				mask := uint32(1) << i
				valid = seen&mask == 0
				seen |= mask
				return valid
			}
		}
		valid = false
		return false
	})
	return valid
}

func (b *laneNativeTerminalBody) classify(data []byte) (laneTerminalKind, string) {
	if !gjson.ValidBytes(data) {
		return laneInvalid, ""
	}
	obj := gjson.ParseBytes(data)
	if !laneTerminalObject(obj, "id", "model", "object", "created", "choices", "usage") ||
		obj.Get("object").Type != gjson.String || obj.Get("object").Str != "chat.completion.chunk" ||
		obj.Get("model").Type != gjson.String || obj.Get("model").Str != b.model {
		return laneInvalid, ""
	}
	id, created := obj.Get("id"), obj.Get("created")
	ts, err := strconv.ParseInt(created.Raw, 10, 64)
	if id.Type != gjson.String || id.Str == "" || len(id.Str) > 128 || id.Str != b.id && b.id != "" ||
		created.Type != gjson.Number || err != nil || ts <= 0 || b.created != 0 && ts != b.created {
		return laneInvalid, ""
	}
	b.id, b.created = id.Str, ts
	choices := obj.Get("choices")
	if !choices.IsArray() {
		return laneInvalid, ""
	}
	cs := choices.Array()
	usage := obj.Get("usage")
	if len(cs) == 0 && usage.IsObject() && len(usage.Raw) <= 4096 {
		return laneUsage, usage.Raw
	}
	if len(cs) != 1 || usage.Type != gjson.Null {
		return laneInvalid, ""
	}
	c := cs[0]
	if !laneTerminalObject(c, "index", "delta", "finish_reason") || c.Get("index").Raw != "0" {
		return laneInvalid, ""
	}
	d, f := c.Get("delta"), c.Get("finish_reason")
	if !laneTerminalObject(d, "role", "content") {
		return laneInvalid, ""
	}
	role, content := d.Get("role"), d.Get("content")
	if role.Exists() && (role.Type != gjson.String || role.Str != "assistant" || b.sawEvent) ||
		content.Exists() && content.Type != gjson.String {
		return laneInvalid, ""
	}
	if f.Type == gjson.String && (f.Str == "stop" || f.Str == "length") && !role.Exists() && !content.Exists() && b.sawContent {
		return laneFinish, ""
	}
	if f.Raw != "null" || (!role.Exists() && !content.Exists()) {
		return laneInvalid, ""
	}
	b.sawContent = b.sawContent || content.Str != ""
	return laneContent, ""
}

func (b *laneNativeTerminalBody) next() ([]byte, error) {
	data, err := b.event()
	if err != nil {
		return nil, err
	}
	kind, _ := b.classify(data)
	b.sawEvent = true
	if kind == laneContent {
		return b.frame, nil
	}
	if kind != laneFinish {
		return nil, errLaneTerminalStream
	}
	tail := append([]byte(nil), b.frame...)
	data, err = b.event()
	if err != nil {
		return nil, err
	}
	kind, usage := b.classify(data)
	if kind != laneUsage {
		return nil, errLaneTerminalStream
	}
	tail = append(tail, b.frame...)
	data, err = b.event()
	if err != nil || !bytes.Equal(data, []byte("[DONE]")) {
		return nil, errLaneTerminalStream
	}
	// Prove EOF even when trailing garbage arrives in a later network read.
	// No unbounded ReadAll or goroutine draining after the client goes away.
	if _, err = b.reader.ReadByte(); err != io.EOF || b.closeSource() != nil {
		return nil, errLaneTerminalStream
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cancelled || !b.verify([]byte(usage)) {
		return nil, errLaneTerminalStream
	}
	return append(tail, b.frame...), io.EOF
}
