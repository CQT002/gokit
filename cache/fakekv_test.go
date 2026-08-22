package cache_test

import (
	"context"
	"time"

	"github.com/cqt002/gokit/cache"
)

// fakeKV là mock của cache.Loader dùng cho test.
//
// Nó ngắn có chủ ý: độ dài của file này là bằng chứng cho tuyên bố ở godoc của
// package — interface hẹp thì mock được, còn một interface ba mươi method thì
// không ai mock.
type fakeKV struct {
	getErr error
	setErr error
	sets   int

	flight cache.Flight
}

func (f *fakeKV) Flight() *cache.Flight { return &f.flight }

func (f *fakeKV) Get(context.Context, string, any) error { return f.getErr }

func (f *fakeKV) Set(context.Context, string, any, time.Duration) error {
	f.sets++
	return f.setErr
}

// Phần còn lại của interface không được test nào dùng tới.
func (f *fakeKV) SetNX(context.Context, string, any, time.Duration) (bool, error) {
	return false, nil
}
func (f *fakeKV) Del(context.Context, ...string) error                { return nil }
func (f *fakeKV) Exists(context.Context, ...string) (int64, error)    { return 0, nil }
func (f *fakeKV) TTL(context.Context, string) (time.Duration, error)  { return 0, nil }
func (f *fakeKV) Expire(context.Context, string, time.Duration) error { return nil }
func (f *fakeKV) Incr(context.Context, string, int64) (int64, error)  { return 0, nil }
func (f *fakeKV) MGet(context.Context, []string, any) error           { return nil }
func (f *fakeKV) Scan(context.Context, string, uint64, int64) ([]string, uint64, error) {
	return nil, 0, nil
}
