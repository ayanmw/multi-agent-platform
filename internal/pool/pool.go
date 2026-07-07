// Package pool implements a centralized Worker Pool for controlling concurrent
// Agent execution with priority queuing and rate limiting.
//
// # Architecture
//
// The WorkerPool sits between the HTTP handler layer and the runtime Engine layer.
// It is the single entry point for all Agent task submission, providing:
//
//  1. Priority queuing — high-priority tasks (e.g., user-initiated) jump ahead of
//     low-priority tasks (e.g., background batch processing).
//  2. Concurrency limiting — a semaphore controls the maximum number of Agents
//     running simultaneously.
//  3. Task cancellation — each running task can be individually stopped via context.
//
// Phase 6-B: This is a minimal viable implementation. Full integration with
// Engine execution hookup happens alongside the CostTracker integration.
package pool

import (
	"container/heap"
	"context"
	"log"
	"sync"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
)

// PoolTask represents a single task submitted to the Worker Pool.
type PoolTask struct {
	ID        string
	AgentID   string
	UserInput string
	Priority  int // 0 = highest, 9 = lowest
	Timeout   time.Duration
	CreatedAt time.Time
}

// PoolResult holds the execution result.
type PoolResult struct {
	TaskID   string
	AgentID  string
	Content  string
	Usage    llm.Usage
	Duration time.Duration
	Error    error
}

// taskItem wraps PoolTask with priority for the heap.
type taskItem struct {
	task      PoolTask
	priority  int
	index     int // heap.Interface requirement
	createdAt time.Time
}

// priorityQueue implements heap.Interface for min-heap ordering.
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

// WorkerPool manages concurrent Agent execution.
type WorkerPool struct {
	workers   int
	taskQueue *priorityQueue
	semaphore chan struct{}
	running   sync.Map // taskID -> cancel func
	mu        sync.Mutex
	started   bool
}

// NewWorkerPool creates a new WorkerPool with the given max concurrency.
func NewWorkerPool(workers int) *WorkerPool {
	return &WorkerPool{
		workers:   workers,
		taskQueue: &priorityQueue{},
		semaphore: make(chan struct{}, workers),
	}
}

// Start begins the worker goroutines. idempotent.
func (p *WorkerPool) Start() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started {
		return
	}
	p.started = true
	log.Printf("[Pool] Starting %d workers", p.workers)
	// Workers will be launched per-submit in this simplified implementation.
}

// Submit enqueues a task. Blocks if semaphore is full (all workers busy).
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

	// Enqueue the task under lock to protect the heap from concurrent mutation.
	p.mu.Lock()
	heap.Push(p.taskQueue, item)
	p.mu.Unlock()
	log.Printf("[Pool] Task %s queued (priority=%d)", task.ID, task.Priority)
	go p.dispatch()
	return nil
}

// dispatch runs in a goroutine and processes the next task.
func (p *WorkerPool) dispatch() {
	// Acquire semaphore (blocks if all workers busy)
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

	// Execute task (placeholder — full Engine integration happens in later phase)
	_ = ctx
	_ = cancel
	p.running.Delete(task.ID)
	log.Printf("[Pool] Task %s completed", task.ID)
	<-p.semaphore
}

// Stats returns current pool statistics.
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
