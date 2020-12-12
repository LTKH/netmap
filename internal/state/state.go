package state

import (
    "sync"
    "time"
)

type States struct {
    sync.RWMutex
    items           map[string]State
}

type State struct {
    ResultCode      int      `json:"result_code"`           
    ResponseTime    float64  `json:"response_time"`           
    Traceroute      int      `json:"-"`
    EndsAt          int64    `json:"-"`
}

func NewCacheStates() *States {

    cache := States{
        items: make(map[string]State),
    }

    return &cache
}

func (s *States) Set(key string, value State) {

    s.Lock()
    defer s.Unlock()

    if value.EndsAt == 0 {
        value.EndsAt = time.Now().UTC().Unix() + 600
    }

    s.items[key] = value

}

func (s *States) Get(key string) (State, bool) {

    s.RLock()
    defer s.RUnlock()

    item, found := s.items[key]

    if !found {
        return State{}, false
    }

    return item, true
}

func (s *States) Delete(key string) bool {

    s.Lock()
    defer s.Unlock()

    if _, found := s.items[key]; !found {
        return false
    }

    delete(s.items, key)

    return true
}

func (s *States) DelExpiredItems() []string {

    s.Lock()
    defer s.Unlock()

    var keys []string
    for k, v := range s.items {
        if time.Now().UTC().Unix() > v.EndsAt {
            delete(s.items, k)
            keys = append(keys, k)
        }
    }

    return keys
}
