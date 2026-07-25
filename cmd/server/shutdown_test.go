package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// TestShutdownManager_ExecutesInOrder 验证 closer 按注册顺序被调用。
func TestShutdownManager_ExecutesInOrder(t *testing.T) {
	sm := newShutdownManager()
	var order []int
	for i := 1; i <= 3; i++ {
		i := i
		sm.Register(nameForIndex(i), func(ctx context.Context) error {
			order = append(order, i)
			return nil
		})
	}

	sm.totalTimeout = 5 * time.Second
	sm.Shutdown()

	if len(order) != 3 {
		t.Fatalf("expected 3 closers, got %d (%v)", len(order), order)
	}
	for i, v := range order {
		if v != i+1 {
			t.Fatalf("expected order %d at index %d, got %d", i+1, i, v)
		}
	}
}

// TestShutdownManager_HonorsTotalTimeout 验证 closer 阻塞超过总超时时，
// Shutdown 不会无限等待。
func TestShutdownManager_HonorsTotalTimeout(t *testing.T) {
	sm := newShutdownManager()
	sm.totalTimeout = 50 * time.Millisecond

	called := int32(0)
	done := int32(0)
	sm.Register("slow", func(ctx context.Context) error {
		atomic.AddInt32(&called, 1)
		<-ctx.Done()
		atomic.AddInt32(&done, 1)
		return ctx.Err()
	})

	start := time.Now()
	sm.Shutdown()
	elapsed := time.Since(start)

	if atomic.LoadInt32(&called) != 1 {
		t.Fatalf("expected slow closer to be called once, got %d", called)
	}
	if atomic.LoadInt32(&done) != 1 {
		t.Fatalf("expected slow closer to observe ctx done, got %d", done)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("shutdown took too long: %v", elapsed)
	}
}

// TestShutdownManager_ContinuesOnError 验证单个 closer 出错不影响后续 closer。
func TestShutdownManager_ContinuesOnError(t *testing.T) {
	sm := newShutdownManager()
	sm.totalTimeout = 5 * time.Second

	var order []int
	sm.Register("failing", func(ctx context.Context) error {
		order = append(order, 1)
		return errors.New("intentional failure")
	})
	sm.Register("ok", func(ctx context.Context) error {
		order = append(order, 2)
		return nil
	})

	sm.Shutdown()

	if len(order) != 2 || order[0] != 1 || order[1] != 2 {
		t.Fatalf("expected [1 2], got %v", order)
	}
}

// TestShutdownManager_ContinuesAfterTimeout 验证总超时后不再调用剩余 closer。
func TestShutdownManager_ContinuesAfterTimeout(t *testing.T) {
	sm := newShutdownManager()
	sm.totalTimeout = 50 * time.Millisecond

	var secondCalled int32
	sm.Register("slow", func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	sm.Register("second", func(ctx context.Context) error {
		atomic.AddInt32(&secondCalled, 1)
		return nil
	})

	sm.Shutdown()

	if atomic.LoadInt32(&secondCalled) != 0 {
		t.Fatalf("expected second closer not to be called after timeout, got %d", secondCalled)
	}
}

func nameForIndex(i int) string {
	return []string{"", "first", "second", "third"}[i]
}
