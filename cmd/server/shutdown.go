package main

import (
	"context"
	"errors"
	"sync"
	"time"
)

// closerFunc 是参与优雅关闭的子系统需要实现的签名：接收带超时的 context，
// 在 context 到期前完成清理并返回错误（如果有）。
type closerFunc func(ctx context.Context) error

// shutdownManager 统一注册并管理所有需要优雅关闭的子系统。
//
// 设计意图：把 main() 中分散的内联关闭逻辑（signal handler、defer、http.Server
// 等）收敛到一个可测试的组件中，使关闭顺序可控、超时可配、行为可观测。
type shutdownManager struct {
	// closers 保存按注册顺序排列的关闭函数。
	closers []closerFunc

	// totalTimeout 控制整个 Shutdown 过程的最大耗时；超过后强制返回。
	totalTimeout time.Duration

	// mu 保护 closers 切片，允许启动阶段并发注册（虽然当前是顺序初始化）。
	mu sync.Mutex
}

// newShutdownManager 创建一个默认 5 秒总超时的 shutdownManager。
func newShutdownManager() *shutdownManager {
	return &shutdownManager{
		closers:      make([]closerFunc, 0, 8),
		totalTimeout: 5 * time.Second,
	}
}

// Register 注册一个 closer 到关闭链末尾。
// closer 应当按照“先停止接受外部流量，再停止内部 goroutine”的顺序注册。
func (sm *shutdownManager) Register(name string, closer closerFunc) {
	if closer == nil {
		return
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.closers = append(sm.closers, func(ctx context.Context) error {
		log.Infof("server", "[shutdown] closing %s", name)
		err := closer(ctx)
		if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			log.Errorf("server", "[shutdown] %s close error: %v", name, err)
		}
		return err
	})
}

// Shutdown 按注册顺序串行调用所有 closer，使用总超时控制整体耗时。
// 即使某个 closer 返回 error，也会继续调用剩余 closer；总超时后立刻返回。
func (sm *shutdownManager) Shutdown() {
	sm.mu.Lock()
	closers := make([]closerFunc, len(sm.closers))
	copy(closers, sm.closers)
	sm.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), sm.totalTimeout)
	defer cancel()

	log.Infof("server", "%v", "[shutdown] starting graceful shutdown")
	for _, closer := range closers {
		// 每个 closer 都在同一个总超时 context 下运行；如果总时间已到，直接退出。
		select {
		case <-ctx.Done():
			log.Warnf("server", "[shutdown] total timeout (%v) exceeded, aborting remaining closers", sm.totalTimeout)
			return
		default:
		}
		_ = closer(ctx)
	}
	log.Infof("server", "%v", "[shutdown] graceful shutdown complete")
}
