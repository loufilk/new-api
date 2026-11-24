package requestplugin

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/QuantumNous/new-api/pkg/requestpatch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/semaphore"
)

var pixelBudgetDecoderID atomic.Uint64

// Register a deterministic decoder at image.Decode's real dispatch boundary.
// Header reads do not decode pixels; the callback observes actual decode entry.
// Unique magic keeps these test-only images separate from real image formats.
func pixelBudgetImage(t *testing.T, name string, pixels int, decode func() (image.Image, error)) inlineMedia {
	t.Helper()
	magic := "pixel-budget:" + t.Name() + ":" + name + ":" + strconv.FormatUint(pixelBudgetDecoderID.Add(1), 10) + ":"
	image.RegisterFormat("png", magic, func(io.Reader) (image.Image, error) {
		return decode()
	}, func(io.Reader) (image.Config, error) {
		return image.Config{ColorModel: color.NRGBAModel, Width: pixels, Height: 1}, nil
	})
	return inlineMedia{path: "/" + name, mime: "image/png", data: base64.StdEncoding.EncodeToString([]byte(magic + strings.Repeat("x", 256)))}
}

func setPixelBudget(t *testing.T, pixels int64) {
	t.Helper()
	previous := processImagePixelBudget
	budget := semaphore.NewWeighted(pixels)
	processImagePixelBudget = func() *semaphore.Weighted { return budget }
	t.Cleanup(func() { processImagePixelBudget = previous })
}

func TestMediaReencodePixelBudgetAcrossRequests(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		setPixelBudget(t, 10)
		pool := imageReencodePool{slots: semaphore.NewWeighted(4), encode: reencodeMedia}
		started := make(chan string, 5)
		var active, peak atomic.Int64
		type job struct {
			media  inlineMedia
			finish chan struct{}
		}
		jobs := make(map[string]job)
		for name, pixels := range map[string]int{"a": 6, "b": 4, "canceled": 1, "big": 8, "small": 2} {
			finish := make(chan struct{})
			media := pixelBudgetImage(t, name, pixels, func() (image.Image, error) {
				n := active.Add(int64(pixels))
				for previous := peak.Load(); n > previous && !peak.CompareAndSwap(previous, n); previous = peak.Load() {
				}
				defer active.Add(-int64(pixels))
				started <- name
				<-finish
				return image.NewNRGBA(image.Rect(0, 0, pixels, 1)), nil
			})
			jobs[name] = job{media: media, finish: finish}
		}
		defer func() {
			for _, job := range jobs {
				select {
				case <-job.finish:
				default:
					close(job.finish)
				}
			}
		}()
		submit := func(ctx context.Context, name string) <-chan []mediaReencodeResult {
			done := make(chan []mediaReencodeResult, 1)
			go func() {
				done <- pool.reencode(ctx, []inlineMedia{jobs[name].media}, []requestpatch.Effect{{Type: requestpatch.EffectMediaReencode, Format: "png"}})
			}()
			synctest.Wait()
			return done
		}
		a := submit(t.Context(), "a")
		require.Equal(t, "a", <-started)
		b := submit(t.Context(), "b")
		require.Equal(t, "b", <-started)
		ctx, cancel := context.WithCancel(t.Context())
		canceled := submit(ctx, "canceled")
		big := submit(t.Context(), "big")
		small := submit(t.Context(), "small")
		require.Empty(t, started, "pixel decoders must wait, despite available CPU slots")
		cancel()
		require.ErrorIs(t, (<-canceled)[0].err, context.Canceled)
		synctest.Wait()
		require.Empty(t, started, "a canceled pixel waiter must never start decoding")

		close(jobs["a"].finish)
		require.NoError(t, (<-a)[0].err)
		synctest.Wait()
		require.Empty(t, started, "later small images must not starve a queued larger image")
		close(jobs["b"].finish)
		require.NoError(t, (<-b)[0].err)
		synctest.Wait()
		require.Len(t, started, 2)
		assert.ElementsMatch(t, []string{"big", "small"}, []string{<-started, <-started})
		assert.EqualValues(t, 10, active.Load())
		close(jobs["big"].finish)
		close(jobs["small"].finish)
		require.NoError(t, (<-big)[0].err)
		require.NoError(t, (<-small)[0].err)
		assert.Zero(t, active.Load())
		assert.EqualValues(t, 10, peak.Load(), "aggregate source pixels admitted to decoding are bounded")
	})
}

func TestMediaReencodePixelBudgetReleasesOnFailure(t *testing.T) {
	for _, failure := range []string{"decode", "panic", "encode"} {
		t.Run(failure, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				setPixelBudget(t, 4)
				pool := imageReencodePool{slots: semaphore.NewWeighted(1), encode: reencodeMedia}
				bad := pixelBudgetImage(t, "bad", 4, func() (image.Image, error) {
					switch failure {
					case "decode":
						return nil, errors.New("decode failed")
					case "panic":
						panic("decode panicked")
					}
					return image.NewNRGBA(image.Rect(0, 0, 4, 1)), nil
				})
				format := "png"
				if failure == "encode" {
					format = "unsupported"
				}
				results := pool.reencode(t.Context(), []inlineMedia{bad}, []requestpatch.Effect{{Format: format}})
				require.Error(t, results[0].err)
				good := pixelBudgetImage(t, "good", 4, func() (image.Image, error) {
					return image.NewNRGBA(image.Rect(0, 0, 4, 1)), nil
				})
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				results = pool.reencode(ctx, []inlineMedia{good}, []requestpatch.Effect{{Format: "png"}})
				require.NoError(t, results[0].err, "failure must release both CPU and pixel permits")
				require.NotEmpty(t, results[0].ops)
			})
		})
	}
}

func TestMediaReencodePixelBudgetKeepsIndividualLimit(t *testing.T) {
	setPixelBudget(t, maxReencodePixels)
	var decoded atomic.Int64
	oversized := pixelBudgetImage(t, "oversized", maxReencodePixels+1, func() (image.Image, error) {
		decoded.Add(1)
		return nil, errors.New("must not decode oversized image")
	})
	pool := imageReencodePool{slots: semaphore.NewWeighted(1), encode: reencodeMedia}
	results := pool.reencode(t.Context(), []inlineMedia{oversized}, []requestpatch.Effect{{Format: "png", MaxSide: 16}})
	require.NoError(t, results[0].err)
	assert.Empty(t, results[0].ops, "over-limit image remains unchanged, even when resize is requested")
	assert.Zero(t, decoded.Load())

	boundary := pixelBudgetImage(t, "boundary", maxReencodePixels, func() (image.Image, error) {
		decoded.Add(1)
		return nil, errors.New("test decoder reached")
	})
	results = pool.reencode(t.Context(), []inlineMedia{boundary}, []requestpatch.Effect{{Format: "png"}})
	require.ErrorContains(t, results[0].err, "cannot be decoded")
	assert.EqualValues(t, 1, decoded.Load(), "an image exactly at the individual limit is still admitted")
}

func TestMediaReencodePixelBudgetPlanCancellation(t *testing.T) {
	setupPlugins(t, `[{"id":"rp","name":"Rp","plugin":"rp-stages","enabled":true,"global":true,"config":{"effects":{"outbound":[{"type":"media.reencode","media":0,"format":"png"}]}}}]`)
	setPixelBudget(t, 4)
	budget := processImagePixelBudget()
	require.NoError(t, budget.Acquire(t.Context(), 4))
	defer budget.Release(4)
	var headers, decodes atomic.Int64
	ready := make(chan struct{})
	magic := "pixel-budget-plan-cancel:" + strconv.FormatUint(pixelBudgetDecoderID.Add(1), 10) + ":"
	image.RegisterFormat("png", magic, func(io.Reader) (image.Image, error) {
		decodes.Add(1)
		return image.NewNRGBA(image.Rect(0, 0, 4, 1)), nil
	}, func(io.Reader) (image.Config, error) {
		// The outbound plugin first reads dimensions. The second header read
		// is reencodeMedia, immediately before pixel admission.
		if headers.Add(1) == 2 {
			close(ready)
		}
		return image.Config{ColorModel: color.NRGBAModel, Width: 4, Height: 1}, nil
	})
	body := []byte(`{"image":"data:image/png;base64,` + base64.StdEncoding.EncodeToString([]byte(magic+strings.Repeat("x", 256))) + `"}`)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan Outcome, 1)
	go func() { done <- NewPlan().Run(ctx, Call{Stage: StageOutbound, Body: body}) }()
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("outbound plugin did not reach pixel admission")
	}
	cancel()
	result := <-done
	require.NotNil(t, result.Failed)
	assert.Equal(t, errorMedia, result.Failed.Class)
	assert.Contains(t, result.Failed.Detail, "context canceled")
	assert.Equal(t, body, result.Body, "cancellation must not publish partial image edits")
	assert.Zero(t, decodes.Load(), "Plan.Run must not decode a canceled pixel waiter")
}

// Benchmark the unchanged real PNG-to-JPEG path with no contention. The pixel
// budget is an admission guard, not a claim of increased encoding throughput.
func BenchmarkMediaReencodePixelBudget(b *testing.B) {
	source := image.NewNRGBA(image.Rect(0, 0, 1280, 720))
	for y := range 720 {
		for x := range 1280 {
			source.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: uint8(x + y), A: 255})
		}
	}
	var buffer bytes.Buffer
	require.NoError(b, (&png.Encoder{CompressionLevel: png.NoCompression}).Encode(&buffer, source))
	item := inlineMedia{path: "/image", mime: "image/png", data: base64.StdEncoding.EncodeToString(buffer.Bytes())}
	effect := requestpatch.Effect{Format: "jpeg", Quality: 75}
	b.ReportAllocs()
	for b.Loop() {
		_, _, err := reencodeMedia(context.Background(), item, effect)
		if err != nil {
			b.Fatal(err)
		}
	}
}
