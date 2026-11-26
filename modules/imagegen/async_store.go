package imagegen

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/redis/go-redis/v9"
)

const AsyncImageTimeout = 5 * time.Minute
const AsyncImageTTL = 15 * time.Minute

// A receipt outlives the result, worker deadline and the admission window. An
// operation ID includes its issue time, so its consent cannot be reused even
// after the receipt expires. Redis, not the browser or node clock, decides.
const asyncOperationTTL = 24 * time.Hour
const asyncOperationWindow = 2 * time.Minute

// The signed token fits the existing 48 hex-character route identifier:
// 6 bytes of Redis time, 8 random bytes, 10 HMAC bytes bound to the owner.
const operationPayloadLen = 28
const operationTagLen = 10

var errOperationConflict = errors.New("image operation bound to different input")
var errOperationExpired = errors.New("image operation can no longer be submitted")
var errOperationPending = errors.New("image operation admission not yet confirmed")
var errOperationMissing = errors.New("image operation unavailable after admission window")
var errOperationClock = errors.New("image operation clock temporarily unavailable")

func operationTime(id string) (int64, error) {
	if len(id) != 48 || strings.ToLower(id) != id {
		return 0, errors.New("invalid image operation")
	}
	if _, err := hex.DecodeString(id); err != nil {
		return 0, err
	}
	return strconv.ParseInt(id[:12], 16, 64)
}

func operationTag(userID int, payload string) []byte {
	// CRYPTO_SECRET is already shared by nodes of a configured multi-node deployment.
	// Domain separation prevents these public IDs from becoming valid in other uses.
	mac := hmac.New(sha256.New, []byte(common.CryptoSecret))
	fmt.Fprintf(mac, "new-api/image-async/owner-operation/v2:%d:%s", userID, payload)
	return mac.Sum(nil)[:operationTagLen]
}

func operationSigned(userID int, id string) bool {
	// Caller validates exact length, case and hex before invoking.
	tag, err := hex.DecodeString(id[operationPayloadLen:])
	return err == nil && hmac.Equal(tag, operationTag(userID, id[:operationPayloadLen]))
}

// The dashboard submits at most four images. Reserve enough response space for
// each image before invoking the paid relay, rather than sharing an 8 MiB cap
// across the whole batch. This is a transport bound, not a resolution promise.
const AsyncImageMaxCount = 4
const AsyncImageResultPerImage = 48 << 20
const AsyncImageMaxResult = AsyncImageMaxCount * AsyncImageResultPerImage
const AsyncImageRetainedBudget = 256 << 20
const asyncImageEnvelopeAllowance = 4096

var ErrAsyncImageBusy = errors.New("async image capacity reached")

type AsyncImageTask struct {
	ID           string          `json:"id"`
	Status       string          `json:"status"`
	Deadline     int64           `json:"deadline"`
	HTTPStatus   int             `json:"http_status,omitempty"`
	RequestID    string          `json:"request_id,omitempty"`
	BillingState string          `json:"billing_state,omitempty"`
	Result       json.RawMessage `json:"result,omitempty"`
	ResultLimit  int             `json:"-"`
}

type AsyncImageStore struct{ Client redis.UniversalClient }

func asyncImageKey(userID int, id string) string {
	return fmt.Sprintf("image-async:{tasks}:result:%d:%s", userID, id)
}

// issueOperation mints a stateless owner-bound ticket; it neither reserves
// result memory nor invokes the relay. Redis supplies the clock used by both
// Lua paths, so a browser's local time cannot push an intent into the future.
func (s AsyncImageStore) issueOperation(ctx context.Context, userID int) (string, error) {
	if s.Client == nil {
		return "", errors.New("async image storage unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	stamp, err := s.Client.Time(ctx).Result()
	if err != nil {
		return "", err
	}
	millis := stamp.UnixMilli()
	if millis < 0 || millis >= 1<<48 {
		return "", errOperationClock
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	payload := fmt.Sprintf("%012x%s", millis, hex.EncodeToString(nonce[:]))
	return payload + hex.EncodeToString(operationTag(userID, payload)), nil
}

// Admission is atomic across instances and compatible with old workers. Each
// index member represents at most 8 MiB. A larger task also occupies expiring
// placeholder members, so old workers' 32-member limit cannot overbook the
// original 256 MiB budget during a rolling upgrade. Completion releases only
// unused members; retained results continue to consume capacity until expiry.
var admitAsyncImage = redis.NewScript(`
-- Validate idempotency before availability and capacity. Never refresh the
-- receipt TTL on retry, and never dispatch a second worker, even after Finish.
if ARGV[9] ~= '' then
  local binding = redis.call('GET', KEYS[5])
  if binding then
    if binding ~= ARGV[9] then return -1 end
    if redis.call('EXISTS', KEYS[3]) == 0 then return -2 end
    return 2
  end
  if ARGV[12] ~= '1' then return -2 end
  if redis.call('EXISTS', KEYS[3]) == 1 then return -1 end
  local tm = redis.call('TIME')
  local now = tonumber(tm[1])*1000 + math.floor(tonumber(tm[2])/1000)
  if now >= tonumber(ARGV[10])+tonumber(ARGV[11]) then return -2 end
  if now < tonumber(ARGV[10]) then return -3 end
end
if ARGV[14] == '0' then return 0 end
-- Old admission can remove expired index entries before a new worker sees
-- them, so sweep reservation metadata independently as well.
for _, field in ipairs(redis.call('HKEYS', KEYS[4])) do
  local key = field
  if string.sub(field, -5) == ':done' then key = string.sub(field, 1, -6) end
  if redis.call('EXISTS', key) == 0 then redis.call('HDEL', KEYS[4], field) end
end
for _, key in ipairs(redis.call('ZRANGE', KEYS[1], 0, -1)) do
  if redis.call('EXISTS', key) == 0 then
    redis.call('ZREM', KEYS[1], key)
    redis.call('HDEL', KEYS[4], key)
    redis.call('HDEL', KEYS[4], key .. ':done')
  end
end
local slots = math.ceil(tonumber(ARGV[6]) / tonumber(ARGV[8]))
if redis.call('ZCARD', KEYS[1]) + slots > tonumber(ARGV[7]) / tonumber(ARGV[8]) or redis.call('EXISTS', KEYS[2]) == 1 then return 0 end
if ARGV[9] ~= '' then redis.call('SET', KEYS[5], ARGV[9], 'PX', ARGV[13]) end
redis.call('SET', KEYS[2], ARGV[3], 'PX', ARGV[4])
redis.call('SET', KEYS[3], ARGV[5], 'PX', ARGV[4])
redis.call('ZADD', KEYS[1], ARGV[2], KEYS[3])
for slot = 1, slots - 1 do
  local key = KEYS[3] .. ':slot:' .. slot
  redis.call('SET', key, '1', 'PX', ARGV[4])
  redis.call('ZADD', KEYS[1], ARGV[2], key)
end
redis.call('HSET', KEYS[4], KEYS[3], ARGV[6])
redis.call('PEXPIRE', KEYS[1], ARGV[4])
redis.call('PEXPIRE', KEYS[4], ARGV[4])
return 1`)

func (s AsyncImageStore) Create(ctx context.Context, userID, count int) (*AsyncImageTask, error) {
	// Legacy clients have server-generated tickets and keep their original route.
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	task, _, err := s.admit(ctx, userID, count, hex.EncodeToString(nonce[:]), "", true)
	return task, err
}

func receiptKey(userID int, id string) string {
	return fmt.Sprintf("image-async:{tasks}:receipt:%d:%s", userID, id)
}

// created=false means the same consent was admitted earlier (possibly by
// another gateway). An ambiguous Redis reply must never dispatch a worker.
func (s AsyncImageStore) admit(ctx context.Context, userID, count int, id, binding string, slot bool) (*AsyncImageTask, bool, error) {
	if count < 1 || count > AsyncImageMaxCount {
		return nil, false, fmt.Errorf("async image count must be between 1 and %d", AsyncImageMaxCount)
	}
	if s.Client == nil {
		return nil, false, errors.New("async image storage unavailable")
	}
	var issued int64
	var signed bool
	if binding != "" {
		var err error
		issued, err = operationTime(id)
		if err != nil {
			return nil, false, err
		}
		signed = operationSigned(userID, id)
	}
	task := &AsyncImageTask{ID: id, Status: "running", Deadline: time.Now().Add(AsyncImageTimeout).Unix(), ResultLimit: count * AsyncImageResultPerImage}
	data, err := common.Marshal(task)
	if err != nil {
		return nil, false, err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	accepted, err := admitAsyncImage.Run(ctx, s.Client, []string{"image-async:{tasks}:index", fmt.Sprintf("image-async:{tasks}:active:%d", userID), asyncImageKey(userID, id), "image-async:{tasks}:reservations", receiptKey(userID, id)}, time.Now().UnixMilli(), time.Now().Add(AsyncImageTTL).UnixMilli(), id, AsyncImageTTL.Milliseconds(), data, task.ResultLimit+asyncImageEnvelopeAllowance, AsyncImageRetainedBudget, 8<<20, binding, issued, asyncOperationWindow.Milliseconds(), signed, asyncOperationTTL.Milliseconds(), slot).Int()
	if err != nil {
		return nil, false, err
	}
	switch accepted {
	case 1:
		return task, true, nil
	case 2:
		return &AsyncImageTask{ID: id, Status: "accepted"}, false, nil
	case -1:
		return nil, false, errOperationConflict
	case -2:
		return nil, false, errOperationExpired
	case -3:
		return nil, false, errOperationClock
	default:
		return nil, false, ErrAsyncImageBusy
	}
}

var readOperation = redis.NewScript(`
local binding=redis.call('GET',KEYS[2])
if binding then
  local result=redis.call('GET',KEYS[1])
  if result then return {'ok',result} end
  return {'expired'}
end
if ARGV[3] ~= '1' then return {'missing'} end
local tm=redis.call('TIME')
local now=tonumber(tm[1])*1000+math.floor(tonumber(tm[2])/1000)
if now < tonumber(ARGV[1]) then return {'clock'} end
if now < tonumber(ARGV[1])+tonumber(ARGV[2]) then return {'pending'} end
return {'missing'}
`)

// An early GET may race a delayed POST. A read-only "pending" is not a
// terminal not-found. Missing is conclusive only once no admission can follow.
func (s AsyncImageStore) getOperation(ctx context.Context, userID int, id string) (*AsyncImageTask, error) {
	issued, err := operationTime(id)
	if err != nil {
		return nil, errOperationMissing
	}
	if s.Client == nil {
		return nil, errors.New("async image storage unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	values, err := readOperation.Run(ctx, s.Client, []string{asyncImageKey(userID, id), receiptKey(userID, id)}, issued, asyncOperationWindow.Milliseconds(), operationSigned(userID, id)).StringSlice()
	if err != nil {
		return nil, err
	}
	switch values[0] {
	case "ok":
		return decodeAsyncTask([]byte(values[1]))
	case "pending":
		return nil, errOperationPending
	case "expired":
		return nil, errOperationExpired
	case "clock":
		return nil, errOperationClock
	default:
		return nil, errOperationMissing
	}
}

func (s AsyncImageStore) Get(ctx context.Context, userID int, id string) (*AsyncImageTask, error) {
	if s.Client == nil {
		return nil, errors.New("async image storage unavailable")
	}
	if len(id) != 48 {
		return nil, redis.Nil
	}
	if _, err := hex.DecodeString(id); err != nil {
		return nil, redis.Nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	data, err := s.Client.Get(ctx, asyncImageKey(userID, id)).Bytes()
	if err != nil {
		return nil, err
	}
	return decodeAsyncTask(data)
}

func decodeAsyncTask(data []byte) (*AsyncImageTask, error) {
	var task AsyncImageTask
	if err := common.Unmarshal(data, &task); err != nil {
		return nil, err
	}
	if task.Status == "running" && time.Now().Unix() > task.Deadline {
		task.Status, task.HTTPStatus = "failed", 504
		task.Result = json.RawMessage(`{"error":{"message":"Generation interrupted or timed out; check usage before submitting again.","code":"async_image_interrupted"}}`)
	}
	return &task, nil
}

var finishAsyncImage = redis.NewScript(`
local ttl = redis.call('PTTL', KEYS[1])
if ttl > 0 then
  if redis.call('HGET', KEYS[3], KEYS[1] .. ':done') == '1' then return 1 end
  local reserved = tonumber(redis.call('HGET', KEYS[3], KEYS[1]) or ARGV[3])
  local data = ARGV[1]
  if string.len(data) > reserved then data = ARGV[5] end
  local size = string.len(data)
  if size > reserved then return redis.error_reply('async image error envelope exceeds its reserved result budget') end
  redis.call('SET', KEYS[1], data, 'PX', ttl)
  local slots = math.ceil(reserved / tonumber(ARGV[3]))
  local needed = math.max(1, math.ceil(size / tonumber(ARGV[3])))
  for slot = needed, slots - 1 do
    local key = KEYS[1] .. ':slot:' .. slot
    redis.call('DEL', key)
    redis.call('ZREM', KEYS[4], key)
  end
  redis.call('HSET', KEYS[3], KEYS[1], size)
  if ARGV[4] ~= 'running' or data == ARGV[5] then redis.call('HSET', KEYS[3], KEYS[1] .. ':done', '1') end
end
if redis.call('GET', KEYS[2]) == ARGV[2] then redis.call('DEL', KEYS[2]) end
return 1`)

func (s AsyncImageStore) Finish(userID int, task *AsyncImageTask) error {
	failure := *task
	failure.Status, failure.HTTPStatus = "failed", 502
	if len(failure.RequestID) > 128 {
		failure.RequestID = ""
	}
	failure.Result = []byte(`{"error":{"message":"Image task response exceeds its reserved result limit; check usage before submitting again.","code":"async_image_result_invalid"}}`)
	fallback, err := common.Marshal(&failure)
	if err != nil {
		return err
	}
	if len(task.Result) > AsyncImageMaxResult || (task.ResultLimit > 0 && len(task.Result) > task.ResultLimit) {
		task = &failure
	}
	data, err := common.Marshal(task)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return finishAsyncImage.Run(ctx, s.Client, []string{asyncImageKey(userID, task.ID), fmt.Sprintf("image-async:{tasks}:active:%d", userID), "image-async:{tasks}:reservations", "image-async:{tasks}:index"}, data, task.ID, 8<<20, task.Status, fallback).Err()
}
