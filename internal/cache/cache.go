package cache

import (
    "time"
    "sync"
    //"net"
    "fmt"
    //"io"
    //"crypto/sha1"
    //"encoding/hex"
    "github.com/ltkh/netmap/internal/config"
)

type Records struct {
    sync.RWMutex
    items          map[string]config.SockTable
    index          map[string]map[string]bool
    limit          int
    flush          time.Duration
}

type Statistics struct {
    Total          int
    Disabled       int
}

func NewCacheRecords(limit int) *Records {
    cache := Records{
        items: make(map[string]config.SockTable),
        limit: limit,
    }
    return &cache
}

func (t *Records) Set(key string, val config.SockTable, timestamp int64) error {
    t.Lock()
    defer t.Unlock()

    _, found := t.items[key]
    if !found && len(t.items) >= t.limit {
        return fmt.Errorf("cache limit exceeded, id: %v", key)
    }

    val.Id = key
    val.Timestamp = timestamp
    t.items[key] = val

    return nil
}

func (t *Records) Get(key string) (config.SockTable, bool) {
    t.RLock()
    defer t.RUnlock()

    val, found := t.items[key]
    if !found {
        return config.SockTable{}, false
    }

    return val, true
}

func (t *Records) Del(key string) bool {
    _, found := t.Get(key)
    if !found {
        return false
    }

    t.Lock()
    defer t.Unlock()

    delete(t.items, key)

    return true
}

func (t *Records) Items() []config.SockTable {
    t.RLock()
    defer t.RUnlock()

    var items []config.SockTable

    for _, val := range t.items {
        items = append(items, val)
    }
    return items

}

func (t *Records) DelExpiredItems(timestamp int64) int {
    t.Lock()
    defer t.Unlock()

    cnt := 0

    for key, val := range t.items {
        if val.Timestamp < timestamp {
            delete(t.items, key)
            cnt = cnt +1
        }
    }

    return cnt
}
