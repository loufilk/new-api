package lifecycle

import (
	"context"
	"testing"
	"time"
)

func TestWorkerFailureDuringDrainRemainsObservable(t *testing.T) {
	group := NewGroup()
	entered := make(chan struct{})
	group.Go("late-failure", func(schedule, _ context.Context) {
		close(entered)
		<-schedule.Done()
		panic("private panic detail")
	})
	<-entered
	group.BeginDrain()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := group.Wait(ctx); err == nil || err.Error() != "background worker late-failure panicked" {
		t.Fatalf("drain lost a late worker failure: %v", err)
	}
	// Application shutdown may already have consumed the failure notification;
	// the joined result must remain available for the final process exit code.
	<-group.Failures()
	if err := group.Wait(context.Background()); err == nil || err.Error() != "background worker late-failure panicked" {
		t.Fatalf("reading the notification cleared the joined result: %v", err)
	}
}
