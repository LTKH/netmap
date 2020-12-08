package state

import (
    "sync"
)

type States struct {
    sync.RWMutex
    items           map[string]State
}

type State struct {
    ResultCode      int      `json:"result_code"`           
    ResponseTime    float64  `json:"response_time"`           
    Traceroute      int      `json:"traceroute"`
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
