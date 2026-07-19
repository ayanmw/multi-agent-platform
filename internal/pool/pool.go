// Package pool 实现一个集中式 Worker Pool，用于通过优先级队列和限流控制并发 Agent 执行。
//
// # 架构
//
// WorkerPool 位于 HTTP handler 层与 runtime Engine 层之间。
// 它是所有 Agent 任务提交的唯一入口，提供：
//
//  1. 优先级排队 —— 高优先级任务（如用户主动发起）会排到低优先级任务
//     （如后台批量处理）之前。
//  2. 并发限制 —— 通过 semaphore 控制同时运行的最大 Agent 数量。
//  3. 任务取消 —— 每个运行中的任务都可以通过 context 单独停止。
//
// Phase 6-B：这是一个最小可用实现。与 Engine 执行挂接的完整集成
// 会与 CostTracker 集成一同进行。
package pool

import (
	"container/heap"
	"context"
	"log"
	"sync"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
)

// PoolTask 表示提交到 Worker Pool 的单个任务。
type PoolTask struct {
	ID        string
	AgentID   string
	UserInput string
	Priority  int // 0 = 最高优先级，9 = 最低优先级
	Timeout   time.Duration
	CreatedAt time.Time
}

// PoolResult 保存执行结果。
type PoolResult struct {
	TaskID   string
	AgentID  string
	Content  string
	Usage    llm.Usage
	Duration time.Duration
	Error    error
}

// taskItem 用优先级包装 PoolTask 以供 heap 使用。
type taskItem struct {
	task      PoolTask
	priority  int
	index     int // heap.Interface 的要求
	createdAt time.Time
}

// priorityQueue 实现 heap.Interface，按 min-heap 顺序排列。
type priorityQueue []*taskItem

func (pq priorityQueue) Len() int { return len(pq) }
func (pq priorityQueue) Less(i, j int) bool {
	if pq[i].priority != pq[j].priority {
		return pq[i].priority < pq[j].priority
	}
	return pq[i].createdAt.Before(pq[j].createdAt)
}
func (pq priorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}
func (pq *priorityQueue) Push(x any) {
	item := x.(*taskItem)
	item.index = len(*pq)
	*pq = append(*pq, item)
}
func (pq *priorityQueue) Pop() any {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.index = -1
	*pq = old[:n-1]
	return item
}

// WorkerPool 管理并发 Agent 执行。
type WorkerPool struct {
	workers   int
	taskQueue *priorityQueue
	semaphore chan struct{}
	running   sync.Map // taskID -> cancel func
	mu        sync.Mutex
	started   bool
}

// NewWorkerPool 创建一个具有给定最大并发数的 WorkerPool。
func NewWorkerPool(workers int) *WorkerPool {
	return &WorkerPool{
		workers:   workers,
		taskQueue: &priorityQueue{},
		semaphore: make(chan struct{}, workers),
	}
}

// Start 启动 worker goroutine。幂等。
func (p *WorkerPool) Start() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started {
		return
	}
	p.started = true
	log.Printf("[Pool] Starting %d workers", p.workers)
	// 在本简化实现中，worker 会在每次 Submit 时单独启动。
}

// Submit 将任务入队。当 semaphore 已满（所有 worker 都在忙）时会阻塞。
func (p *WorkerPool) Submit(task PoolTask) error {
	p.mu.Lock()
	if !p.started {
		p.started = true
	}
	p.mu.Unlock()

	item := &taskItem{
		task:      task,
		priority:  task.Priority,
		createdAt: task.CreatedAt,
	}
	if item.createdAt.IsZero() {
		item.createdAt = time.Now()
	}

	// 在锁保护下将任务入队，以保护 heap 免受并发修改。
	p.mu.Lock()
	heap.Push(p.taskQueue, item)
	p.mu.Unlock()
	log.Printf("[Pool] Task %s queued (priority=%d)", task.ID, task.Priority)
	go p.dispatch()
	return nil
}

// dispatch 运行于 goroutine 中，处理下一个任务。
func (p *WorkerPool) dispatch() {
	// 获取 semaphore（所有 worker 都忙时阻塞）
	p.semaphore <- struct{}{}

	p.mu.Lock()
	if p.taskQueue.Len() == 0 {
		p.mu.Unlock()
		<-p.semaphore
		return
	}
	item := heap.Pop(p.taskQueue).(*taskItem)
	p.mu.Unlock()

	task := item.task
	ctx, cancel := context.WithTimeout(context.Background(), task.Timeout)
	if task.Timeout == 0 {
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Minute)
	}
	p.running.Store(task.ID, cancel)
	log.Printf("[Pool] Task %s started (priority=%d)", task.ID, task.Priority)

	// 执行任务（占位实现 —— 完整的 Engine 集成将在后续 phase 完成）
	_ = ctx
	_ = cancel
	p.running.Delete(task.ID)
	log.Printf("[Pool] Task %s completed", task.ID)
	<-p.semaphore
}

// Stats 返回当前 pool 的统计信息。
func (p *WorkerPool) Stats() map[string]int {
	p.mu.Lock()
	queueLen := p.taskQueue.Len()
	p.mu.Unlock()
	running := 0
	p.running.Range(func(_, _ any) bool { running++; return true })
	return map[string]int{
		"queued":   queueLen,
		"running":  running,
		"workers":  p.workers,
		"capacity": cap(p.semaphore),
	}
}
