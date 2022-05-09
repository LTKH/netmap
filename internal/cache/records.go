package cache

import (
    "time"
    "sync"
    "net"
)

// SockAddr represents
type SockAddr struct {
    IP             net.IP                 `json:"ip"`
    Name           string                 `json:"name"`
}

// SockTable type represents each line of the /proc/net/[tcp|udp]
type SockTable struct {
    LocalAddr      SockAddr               `json:"localAddr"`
    RemoteAddr     SockAddr               `json:"remoteAddr"`
    Relation       Relation               `json:"relation"`
    Options        Options                `json:"options"`
}

type Relation struct {
    Mode           string                 `json:"mode"`
    Port           uint16                 `json:"port"`
    Result         int                    `json:"result"`
    Response       float64                `json:"response"`
    Trace          int                    `json:"trace"`
}

type Options struct {
    Service        string                 `json:"service"`
    Status         string                 `json:"status"`
    Command        string                 `json:"command"`
    Timeout        time.Duration          `json:"timeout"`
    MaxRespTime    float64                `json:"max_resp_time"`
    ExpireTime     int64                  `json:"expire_time"`
}

type Records struct {
    sync.RWMutex
    items          map[string]SockTable
    limit          int
}

type Statistics struct {
    Total          int
    Disabled       int
}

func NewCacheRecords(limit int) *Records {
    cache := Records{
        items: make(map[string]SockTable),
        limit: limit,
    }
    return &cache
}

func (t *Records) Set(key string, val SockTable) bool {
    t.Lock()
    defer t.Unlock()

    if len(t.items) > t.limit {
        return false
    }
    if val.Options.ExpireTime == 0 {
        val.Options.ExpireTime = time.Now().UTC().Unix() + 900
    }
    t.items[key] = val
    return true
}

func (t *Records) Get(key string) (SockTable, bool) {
    t.RLock()
    defer t.RUnlock()

    val := SockTable{}
    val, found := t.items[key]
    if !found {
        return val, false
    }
    return val, true
}

func (t *Records) Items() map[string]SockTable {
    t.RLock()
    defer t.RUnlock()
    
    items := make(map[string]SockTable)
    for k, v := range t.items {
        items[k] = v
    }  
    return items
}

func (t *Records) DelExpiredItems() bool {

    t.Lock()
    defer t.Unlock()

    for k, v := range t.items {
        if time.Now().UTC().Unix() > v.Options.ExpireTime {
            delete(t.items, k)
        }
    }

    return true
}

func (t *Records) GetStatistics() Statistics {
    t.RLock()
    defer t.RUnlock()

    var stat Statistics
    for _, nr := range t.items {
        stat.Total = stat.Total + 1
        if nr.Options.Status != "" {
            stat.Disabled = stat.Disabled +1
        }
    }
    
    return stat
}