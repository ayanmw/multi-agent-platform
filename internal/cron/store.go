// store.go — Cron Store 接口与薄封装。
//
// 与 internal/todo/store.go 同样的设计：通过 DBStore 接口注入持久化实现，
// 避免 internal/cron 直接 import pkg/db，从而打破
// tool -> cron -> db -> ... 的潜在循环依赖；同时让单元测试可注入 mock。
package cron

import (
	"database/sql"
	"errors"

	"github.com/anmingwei/multi-agent-platform/pkg/event"
)

// EventBus 是 Service/Executor 用来广播 cron_* 事件的抽象。
// 与 runtime.EventBus 签名一致，但不直接依赖 runtime 包，避免循环依赖。
type EventBus interface {
	SendEvent(evt event.Event)
}

// DBStore 声明 Cron 持久化所需的全部 CRUD 操作。
// 默认由 pkg/db 中的 InsertCron/UpdateCron/... 函数实现。
type DBStore interface {
	InsertCron(c Cron) error
	UpdateCron(c Cron) error
	UpdateCronScheduleMeta(c Cron) error
	DeleteCron(id string) error
	GetCron(id string) (Cron, error)
	ListCrons(filter ListFilter) ([]Cron, error)

	InsertExecution(e Execution) error
	UpdateExecution(e Execution) error
	GetExecution(id string) (Execution, error)
	ListExecutions(filter ExecListFilter) ([]Execution, error)
	CleanExecutions(filter CleanFilter) (int, error)
}

// Store 是 DBStore 之上的薄封装，提供与业务语义更贴近的方法名。
// 当前直接转调 DBStore；保留这层是为了未来在 Store 层加缓存/校验时不破坏调用方。
type Store struct {
	db DBStore
}

// NewStore 创建基于给定 DBStore 的 Store。
func NewStore(db DBStore) *Store {
	return &Store{db: db}
}

// 以下方法转调 DBStore，错误透传。

func (s *Store) InsertCron(c Cron) error     { return s.db.InsertCron(c) }
func (s *Store) UpdateCron(c Cron) error     { return s.db.UpdateCron(c) }
func (s *Store) UpdateScheduleMeta(c Cron) error { return s.db.UpdateCronScheduleMeta(c) }
func (s *Store) DeleteCron(id string) error  { return s.db.DeleteCron(id) }
func (s *Store) GetCron(id string) (Cron, error) { return s.db.GetCron(id) }
func (s *Store) ListCrons(f ListFilter) ([]Cron, error) { return s.db.ListCrons(f) }

func (s *Store) InsertExecution(e Execution) error       { return s.db.InsertExecution(e) }
func (s *Store) UpdateExecution(e Execution) error       { return s.db.UpdateExecution(e) }
func (s *Store) GetExecution(id string) (Execution, error) { return s.db.GetExecution(id) }
func (s *Store) ListExecutions(f ExecListFilter) ([]Execution, error) { return s.db.ListExecutions(f) }
func (s *Store) CleanExecutions(f CleanFilter) (int, error) { return s.db.CleanExecutions(f) }

// ErrNotFound 是应用层使用的"记录不存在"哨兵错误。
// pkg/db 侧有自己的 ErrCronNotFound，这里再定义一个供 cron 包内部统一判断，
// 通过 errors.Is 桥接（DBStore 实现返回的底层错误被 errors.Is 识别）。
var ErrNotFound = errors.New("cron: not found")

// IsNotFound 判断错误是否表示"记录不存在"。
// 接受底层 *sql.ErrNoRows 与 pkg/db.ErrCronNotFound/ErrCronExecutionNotFound。
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ErrNotFound) ||
		errors.Is(err, sql.ErrNoRows) ||
		err.Error() == "cron not found" ||
		err.Error() == "cron execution not found"
}
