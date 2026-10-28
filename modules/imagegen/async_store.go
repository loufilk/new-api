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
const AsyncImageMaxResult = 8 << 20

var ErrAsyncImageBusy = errors.New("async image capacity reached")

type AsyncImageTask struct {
	ID         string          `json:"id"`
	Status     string          `json:"status"`
	Deadline   int64           `json:"deadline"`
	HTTPStatus int             `json:"http_status,omitempty"`
	RequestID  string          `json:"request_id,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
}

type AsyncImageStore struct{ Client redis.UniversalClient }

func asyncImageKey(userID int, id string) string {
	return fmt.Sprintf("image-async:{tasks}:result:%d:%s", userID, id)
}

// Admission is atomic across instances. The global bound includes retained
// results, so up to 32 * 8 MiB of image payload can be retained for 15 minutes.
var admitAsyncImage = redis.NewScript(`
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
if redis.call('ZCARD', KEYS[1]) >= 32 or redis.call('EXISTS', KEYS[2]) == 1 then return 0 end
redis.call('SET', KEYS[2], ARGV[3], 'PX', ARGV[4])
redis.call('SET', KEYS[3], ARGV[5], 'PX', ARGV[4])
redis.call('ZADD', KEYS[1], ARGV[2], KEYS[3])
redis.call('PEXPIRE', KEYS[1], ARGV[4])
return 1`)

func (s AsyncImageStore) Create(ctx context.Context, userID int) (*AsyncImageTask, error) {
	if s.Client == nil {
		return nil, errors.New("async image storage unavailable")
	}
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	task := &AsyncImageTask{ID: hex.EncodeToString(nonce[:]), Status: "running", Deadline: time.Now().Add(AsyncImageTimeout).Unix()}
	data, err := common.Marshal(task)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	accepted, err := admitAsyncImage.Run(ctx, s.Client, []string{"image-async:{tasks}:index", fmt.Sprintf("image-async:{tasks}:active:%d", userID), asyncImageKey(userID, task.ID)}, time.Now().UnixMilli(), time.Now().Add(AsyncImageTTL).UnixMilli(), task.ID, AsyncImageTTL.Milliseconds(), data).Int()
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
if ttl > 0 then redis.call('SET', KEYS[1], ARGV[1], 'PX', ttl) end
if redis.call('GET', KEYS[2]) == ARGV[2] then redis.call('DEL', KEYS[2]) end
return 1`)

func (s AsyncImageStore) Finish(userID int, task *AsyncImageTask) error {
	data, err := common.Marshal(task)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return finishAsyncImage.Run(ctx, s.Client, []string{asyncImageKey(userID, task.ID), fmt.Sprintf("image-async:{tasks}:active:%d", userID)}, data, task.ID).Err()
}
