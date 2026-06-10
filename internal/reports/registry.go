package reports

import (
	"context"
	"fmt"
)

type Request struct {
	ReportKey  string
	Version    int
	Format     string
	Parameters map[string]interface{}
	Limit      int
}

type RowSink interface {
	WriteRow(row []interface{}) error
}

type Executor interface {
	Key() string
	Version() int
	Validate(ctx context.Context, req Request) error
	Run(ctx context.Context, req Request, sink RowSink) error
}

type Registry struct {
	executors map[string]Executor
}

func NewRegistry() *Registry {
	return &Registry{
		executors: make(map[string]Executor),
	}
}

func (r *Registry) Register(e Executor) {
	key := fmt.Sprintf("%s_v%d", e.Key(), e.Version())
	r.executors[key] = e
}

func (r *Registry) Get(key string, version int) (Executor, bool) {
	k := fmt.Sprintf("%s_v%d", key, version)
	e, ok := r.executors[k]
	return e, ok
}
