package imagegen

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/redis/go-redis/v9"
)

const AsyncImageTimeout = 5 * time.Minute
const AsyncImageTTL = 15 * time.Minute

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

// Admission is atomic across instances and compatible with old workers. Each
// index member represents at most 8 MiB. A larger task also occupies expiring
// placeholder members, so old workers' 32-member limit cannot overbook the
// original 256 MiB budget during a rolling upgrade. Completion releases only
// unused members; retained results continue to consume capacity until expiry.
var admitAsyncImage = redis.NewScript(`
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
	if count < 1 || count > AsyncImageMaxCount {
		return nil, fmt.Errorf("async image count must be between 1 and %d", AsyncImageMaxCount)
	}
	if s.Client == nil {
		return nil, errors.New("async image storage unavailable")
	}
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	task := &AsyncImageTask{ID: hex.EncodeToString(nonce[:]), Status: "running", Deadline: time.Now().Add(AsyncImageTimeout).Unix(), ResultLimit: count * AsyncImageResultPerImage}
	data, err := common.Marshal(task)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	accepted, err := admitAsyncImage.Run(ctx, s.Client, []string{"image-async:{tasks}:index", fmt.Sprintf("image-async:{tasks}:active:%d", userID), asyncImageKey(userID, task.ID), "image-async:{tasks}:reservations"}, time.Now().UnixMilli(), time.Now().Add(AsyncImageTTL).UnixMilli(), task.ID, AsyncImageTTL.Milliseconds(), data, task.ResultLimit+asyncImageEnvelopeAllowance, AsyncImageRetainedBudget, 8<<20).Int()
	if err != nil {
		return nil, err
	}
	if accepted != 1 {
		return nil, ErrAsyncImageBusy
	}
	return task, nil
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
