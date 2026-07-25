package harness

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anmingwei/multi-agent-platform/pkg/db"
)

// heartbeatFakeMemoryDB 是一个最小的 MemoryDB 实现，用于控制 Beat 是否阻塞。
type heartbeatFakeMemoryDB struct {
	completedIDs []string
	blockBeat    chan struct{}
	beatCount    int32
}

func (f *heartbeatFakeMemoryDB) QueryCompletedTaskIDs(since time.Time) ([]string, error) {
	atomic.AddInt32(&f.beatCount, 1)
	if f.blockBeat != nil {
		<-f.blockBeat
	}
	return f.completedIDs, nil
}
func (f *heartbeatFakeMemoryDB) QueryConversationsByTask(taskID string) ([]db.ConversationRecord, error) {
	return nil, nil
}
func (f *heartbeatFakeMemoryDB) QueryStepsByTaskForMemory(taskID string) ([]db.StepRecord, error) {
	return nil, nil
}
func (f *heartbeatFakeMemoryDB) InsertMemory(record db.MemoryRecord) error { return nil }
func (f *heartbeatFakeMemoryDB) QueryMemoriesByTier(projectID, tier string) ([]db.MemoryRecord, error) {
	return nil, nil
}
func (f *heartbeatFakeMemoryDB) UpdateMemoryTier(id, tier, promotionReason string) error { return nil }

// TestHeartbeat_StopWaitsForGoroutine 验证 Stop 会等待后台 goroutine 退出。
func TestHeartbeat_StopWaitsForGoroutine(t *testing.T) {
	block := make(chan struct{})
	memDB := &heartbeatFakeMemoryDB{completedIDs: []string{"t1"}, blockBeat: block}
	hb := NewHeartbeat(memDB, nil)
	hb.interval = 24 * time.Hour // 避免 ticker 触发
	go hb.Start(context.Background())

	// 等待 Beat 进入 QueryCompletedTaskIDs 阻塞点。
	time.Sleep(100 * time.Millisecond)

	// 在另一个 goroutine 中调用 Stop，避免测试本身阻塞。
	stopped := make(chan struct{})
	go func() {
		hb.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
		t.Fatal("Stop should not return before blocked Beat finishes")
	case <-time.After(200 * time.Millisecond):
		// 符合预期：Stop 正在等待。
	}

	// 放行 Beat，让 goroutine 退出。
	close(block)

	select {
	case <-stopped:
		// 成功。
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return after Beat finished")
	}

	if atomic.LoadInt32(&memDB.beatCount) != 1 {
		t.Fatalf("expected exactly 1 beat, got %d", memDB.beatCount)
	}
}

// TestHeartbeat_NoBeatAfterStop 验证 Stop 返回后不会继续执行 beat。
func TestHeartbeat_NoBeatAfterStop(t *testing.T) {
	memDB := &heartbeatFakeMemoryDB{completedIDs: []string{}}
	hb := NewHeartbeat(memDB, nil)
	hb.interval = 10 * time.Millisecond // 快速触发
	go hb.Start(context.Background())

	// 让 initial beat 跑一次。
	time.Sleep(50 * time.Millisecond)
	hb.Stop()

	count := atomic.LoadInt32(&memDB.beatCount)
	// 给 ticker 一点时间，如果 Stop 无效，beatCount 会继续增长。
	time.Sleep(80 * time.Millisecond)
	if atomic.LoadInt32(&memDB.beatCount) != count {
		t.Fatalf("beat continued after stop: before=%d after=%d", count, atomic.LoadInt32(&memDB.beatCount))
	}
}
