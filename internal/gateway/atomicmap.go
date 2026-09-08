package gateway

import (
	"sync"
	"time"
)

type atomicMap struct {
	m sync.Map
}

func (a *atomicMap) store(k uint, t time.Time) { a.m.Store(k, t) }
func (a *atomicMap) delete(k uint)             { a.m.Delete(k) }
func (a *atomicMap) rangeFn(fn func(uint, time.Time)) {
	a.m.Range(func(k, v any) bool {
		fn(k.(uint), v.(time.Time))
		return true
	})
}
