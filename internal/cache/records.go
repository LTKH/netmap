package cache

import (
    "time"
    "sync"
    "net"
    "fmt"
    "io"
    "crypto/sha1"
    "encoding/hex"
)

// SockAddr represents
type SockAddr struct {
    IP             net.IP                 `json:"ip"`
    Name           string                 `json:"name"`
}

// SockTable type represents each line of the /proc/net/[tcp|udp]
type SockTable struct {
    Id             string                 `json:"id,omitempty"`
    LocalAddr      SockAddr               `json:"localAddr"`
    RemoteAddr     SockAddr               `json:"remoteAddr"`
    Relation       Relation               `json:"relation"`
    Options        Options                `json:"options"`
}

type Relation struct {
    Mode           string                 `json:"mode"`
    Port           uint16                 `json:"port"`
    Command        string                 `json:"command,omitempty"`
    Result         int                    `json:"result"`
    Response       float64                `json:"response"`
    Trace          int                    `json:"trace"`
}

type Options struct {
    Service        string                 `json:"service,omitempty"`
    Status         string                 `json:"status,omitempty"`
    Command        string                 `json:"command,omitempty"`
    Timeout        float64                `json:"timeout"`
    MaxRespTime    float64                `json:"max_resp_time"`
    ActiveTime     int64                  `json:"active_time"`
}

type Records struct {
    sync.RWMutex
    items          map[string]SockTable
    index          map[string]map[string]bool
    limit          int
    flush          time.Duration
}

type Statistics struct {
    Total          int
    Disabled       int
}

func GetHash(text string) string {
    h := sha1.New()
    io.WriteString(h, text)
    return hex.EncodeToString(h.Sum(nil))
}

func GetID(i *SockTable) string {
    return GetHash(fmt.Sprintf("%v:%v:%v:%v:%v:%v", i.LocalAddr.IP, i.LocalAddr.Name, i.RemoteAddr.IP, i.RemoteAddr.Name, i.Relation.Mode, i.Relation.Port))
}

func NewCacheRecords(limit int, flush time.Duration) *Records {
    cache := Records{
        items: make(map[string]SockTable),
        index: make(map[string]map[string]bool),
        limit: limit,
        flush: flush,
    }
    return &cache
}

func (t *Records) Set(key string, val SockTable, active bool) error {
    t.Lock()
    defer t.Unlock()

    _, found := t.items[key]
    if !found && len(t.items) >= t.limit {
        return fmt.Errorf("cache limit exceeded, id: %v", key)
    }

    if active {
        val.Options.ActiveTime = time.Now().UTC().Unix()
    }

    if _, ok := t.index[val.LocalAddr.Name]; !ok {
        t.index[val.LocalAddr.Name] = make(map[string]bool)
    }

    t.index[val.LocalAddr.Name][key] = true
    t.items[key] = val

    return nil
}

func (t *Records) Get(key string) (SockTable, bool) {
    t.RLock()
    defer t.RUnlock()

    val, found := t.items[key]
    if !found {
        return SockTable{}, false
    }

    return val, true
}

func (t *Records) Del(key string) bool {
    val, found := t.Get(key)
    if !found {
        return false
    }

    t.Lock()
    defer t.Unlock()

    if _, ok := t.index[val.LocalAddr.Name]; ok {
        if _, ok := t.index[val.LocalAddr.Name][key]; ok {
            delete(t.index[val.LocalAddr.Name], key)
        }
        if len(t.index[val.LocalAddr.Name]) == 0 {
            delete(t.index, val.LocalAddr.Name)
        }
    }

    delete(t.items, key)

    return true
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

func (t *Records) DelExpiredItems() []string {

    var keys []string

    t.Lock()
    defer t.Unlock()

    for k, v := range t.items {
        if float64(v.Options.ActiveTime) + float64(t.flush / time.Second) < float64(time.Now().UTC().Unix()) {
            delete(t.items, k)
            keys = append(keys, k)
        }
    }

    return keys
}

func (t *Records) GetByName(name string) ([]SockTable, bool) {
    t.RLock()
    defer t.RUnlock()

    arr := []SockTable{}

    if _, ok := t.index[name]; !ok {
        return arr, false
    }

    for key, _ := range t.index[name] {
        if val, ok := t.items[key]; ok {
            arr = append(arr, val)
        }
    }

    return arr, true
}