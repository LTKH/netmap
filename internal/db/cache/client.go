package cache

import (
    //"time"
    "sync"
    //"net"
    //"io"
    "time"
    "errors"
    //"crypto/sha1"
    //"encoding/hex"
    "github.com/ltkh/netmap/internal/config"
)

type Client struct {
    sync.RWMutex
    items          map[string]config.SockTable
    index          map[string]map[string]bool
    limit          int
}

func New(conf *config.DB) (*Client, error) {

    // Set CacheLimit
    if conf.Limit == 0 {
        conf.Limit = 1000000
    }

    client := Client{
        items: make(map[string]config.SockTable),
        index: make(map[string]map[string]bool),
        limit: conf.Limit,
    }
    return &client, nil
}

func (db *Client) Close() error {
    return nil
}

func (db *Client) CreateTables() error {
    return nil
}

func (db *Client) LoadTables() error {
    return nil
}

func (db *Client) SaveStatus(records []config.SockTable) error {
    db.Lock()
    defer db.Unlock()

    for _, rec := range records {

        item, found := db.items[rec.Id]
        if !found {
            continue
        }

        item.Relation = rec.Relation
        item.Timestamp = time.Now().UTC().Unix()

        db.items[rec.Id]= item

    }

    return nil
}

func (db *Client) SaveNetstat(records []config.SockTable) error {
    db.Lock()
    defer db.Unlock()

    for _, rec := range records {

        _, found := db.items[rec.Id]
        if found {
            continue
        }

        if _, ok := db.index[rec.LocalAddr.Name]; !ok {
            db.index[rec.LocalAddr.Name] = make(map[string]bool)
        }

        rec.Timestamp = time.Now().UTC().Unix()
        db.index[rec.LocalAddr.Name][rec.Id] = true
        db.items[rec.Id] = rec
    }

    return nil
}

func (db *Client) LoadRecords(args config.RecArgs) ([]config.SockTable, error) {
    db.RLock()
    defer db.RUnlock()

    var items []config.SockTable

    if args.SrcName == "" {
        for _, val := range db.items {
            items = append(items, val)
        }
        return items, nil
    } 
    
    if _, ok := db.index[args.SrcName]; ok {
        for key, _ := range db.index[args.SrcName] {
            if val, ok := db.items[key]; ok {
                items = append(items, val)
            }
        }
    }

    return items, nil
}

func (db *Client) SaveRecords(records []config.SockTable) error {
    db.Lock()
    defer db.Unlock()

    for _, rec := range records {

        _, found := db.items[rec.Id]
        if !found && len(db.items) >= db.limit {
            return errors.New("cache limit exceeded")
        }

        if _, ok := db.index[rec.LocalAddr.Name]; !ok {
            db.index[rec.LocalAddr.Name] = make(map[string]bool)
        }

        rec.Timestamp = time.Now().UTC().Unix()
        db.index[rec.LocalAddr.Name][rec.Id] = true
        db.items[rec.Id] = rec
        
    }

    return nil
}

func (db *Client) DelRecords(ids []string) error {
    db.Lock()
    defer db.Unlock()

    for _, id := range ids {
        rec, found := db.items[id]
        if !found {
            continue
        }

        if _, ok := db.index[rec.LocalAddr.Name]; ok {
            if _, ok := db.index[rec.LocalAddr.Name][id]; ok {
                delete(db.index[rec.LocalAddr.Name], id)
            }
            if len(db.index[rec.LocalAddr.Name]) == 0 {
                delete(db.index, rec.LocalAddr.Name)
            }
        }
    
        delete(db.items, id)
    }
    
    return nil
}

func (db *Client) LoadExceptions(args config.ExpArgs) ([]config.Exception, error) {
    result := []config.Exception{}
    return result, nil
}

func (db *Client) SaveExceptions(records []config.Exception) error {
    return nil
}

func (db *Client) DelExceptions(ids []string) error {
    return nil
}
