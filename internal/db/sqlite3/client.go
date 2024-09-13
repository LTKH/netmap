package sqlite3

import (
    "os"
    "fmt"
    //"time"
    "sync"
    //"net"
    //"io"
    "time"
    "errors"
    //"regexp"
    //"strings"
    //"crypto/sha1"
    //"encoding/hex"
    "encoding/json"
    "database/sql"
    _ "github.com/mattn/go-sqlite3"
    "github.com/ltkh/netmap/internal/config"
)

type Client struct {
    records    Records 
    exceptions Exceptions
    client     *sql.DB
    config     *config.DB
}

type Records struct {
    sync.RWMutex
    items      map[string]config.SockTable
    index      map[string]map[string]bool
}

type Exceptions struct {
    sync.RWMutex
    items      map[string]config.Exception
}

func New(conf *config.DB) (*Client, error) {

    if _, err := os.Stat(conf.ConnString); errors.Is(err, os.ErrNotExist) {
        _, err := os.Create(conf.ConnString)
        if err != nil {
            return nil, err
        }
    }
    conn, err := sql.Open("sqlite3", conf.ConnString)
    if err != nil {
        return nil, err
    }

    // Set CacheLimit
    if conf.Limit == 0 {
        conf.Limit = 1000000
    }

    client := Client{
        records: Records{
            items: make(map[string]config.SockTable),
            index: make(map[string]map[string]bool),
        },
        exceptions: Exceptions{
            items: make(map[string]config.Exception),
        },
        client: conn, 
        config: conf,
    }

    return &client, nil
}

func (db *Client) Close() error {
    return nil
}

func (db *Client) CreateTables() error {
    _, err := db.client.Exec(
      `create table if not exists records (
        id            varchar(50) primary key,
        timestamp     bigint(20) default 0,
        localName     varchar(50) not null,
        localIP       varchar(20) not null,
        remoteName    varchar(50) not null,
        remoteIP      varchar(20) not null,
        relation      json,
        options       json
      );
      create index if not exists localNameIdx 
        ON records (localName);
      create table if not exists exceptions (
        id            varchar(50) primary key,
        accountId     int default 0,
        hostMask      varchar(50) not null,
        ignoreMask    varchar(50) not null
      );`)
    if err != nil {
        return err
    }

    return nil
}

func (db *Client) LoadTableRecords() error {
    db.records.Lock()
    defer db.records.Unlock()

    sql := "select id,timestamp,localName,localIP,remoteName,remoteIP,relation,options from records order by id"

    rows, err := db.client.Query(sql, nil)
    if err != nil { return err }
    defer rows.Close()

    for rows.Next() {
        var rec config.SockTable
        var relation []uint8
        var options []uint8
        err := rows.Scan(
            &rec.Id, 
            &rec.Timestamp,
            &rec.LocalAddr.Name, 
            &rec.LocalAddr.IP, 
            &rec.RemoteAddr.Name, 
            &rec.RemoteAddr.IP,
            &relation, 
            &options, 
        )
        if err != nil { return err }
        err = json.Unmarshal(relation, &rec.Relation)
        if err != nil { continue }
        err = json.Unmarshal(options, &rec.Options)
        if err != nil { continue }

        db.records.items[rec.Id] = rec 
    }

    return nil
}

func (db *Client) LoadTableExceptions() error {
    db.exceptions.Lock()
    defer db.exceptions.Unlock()

    sql := "select id,accountId,hostMask,ignoreMask from exceptions order by accountId,id"

    rows, err := db.client.Query(sql, nil)
    if err != nil { return err }
    defer rows.Close()

    for rows.Next() {
        var exp config.Exception
        err := rows.Scan(
            &exp.Id, 
            &exp.AccountID,
            &exp.HostMask,
            &exp.IgnoreMask,
        )
        if err != nil { return err }
        db.exceptions.items[exp.Id] = exp
    }

    return nil
}

func (db *Client) LoadTables() error {
    
    if err := db.LoadTableRecords(); err != nil {
        return err
    }
    
    if err := db.LoadTableExceptions(); err != nil {
        return err
    }

    return nil
}

func (db *Client) SaveStatus(records []config.SockTable) error {
    db.records.Lock()
    defer db.records.Unlock()

    for _, rec := range records {

        if rec.Id == "" {
            rec.Id = config.GetIdRec(&rec)
        }

        item, found := db.records.items[rec.Id]
        if !found {
            continue
        }

        item.Relation = rec.Relation
        item.Timestamp = time.Now().UTC().Unix()
        db.records.items[rec.Id] = item

    }

    return nil
}

func (db *Client) SaveNetstat(records []config.SockTable) error {
    db.records.RLock()
    var items []config.SockTable

    for _, rec := range records {

        if rec.Id == "" {
            rec.Id = config.GetIdRec(&rec)
        }

        _, found := db.records.items[rec.Id]
        if found {
            continue
        }

        items = append(items, rec)
    }

    db.records.RUnlock()

    return db.SaveRecords(items)
}

func (db *Client) SaveTracert(records []config.SockTable) error {
    db.records.Lock()
    var items []config.SockTable

    for _, rec := range records {

        if rec.Id == "" {
            rec.Id = config.GetIdRec(&rec)
        }

        item, found := db.records.items[rec.Id]
        if !found {
            continue
        }

        item.Relation.Trace = 2
        
        if rec.Options.Command != "" {
            item.Options.Command = rec.Options.Command
            items = append(items, item)
            continue
        }

        item.Timestamp = time.Now().UTC().Unix()
        db.records.items[rec.Id] = item

    }

    db.records.Unlock()

    return db.SaveRecords(items)
}

func (db *Client) LoadRecords(args config.RecArgs) ([]config.SockTable, error) {
    db.records.RLock()
    defer db.records.RUnlock()

    var items []config.SockTable

    if args.SrcName == "" {
        for _, val := range db.records.items {
            items = append(items, val)
        }
        return items, nil
    } 
    
    if _, ok := db.records.index[args.SrcName]; ok {
        for key, _ := range db.records.index[args.SrcName] {
            if val, ok := db.records.items[key]; ok {
                items = append(items, val)
            }
        }
    }

    return items, nil
}

func (db *Client) SaveRecords(records []config.SockTable) error {
    db.records.Lock()
    defer db.records.Unlock()

    sql := "replace into records (id,timestamp,localName,localIP,remoteName,remoteIP,relation,options) values (?,?,?,?,?,?,?,?)"

    for _, rec := range records {

        rec.Id = config.GetIdRec(&rec)

        _, found := db.records.items[rec.Id]
        if !found && len(db.records.items) >= db.config.Limit {
            return errors.New("cache limit exceeded")
        }

        relation, err := json.Marshal(rec.Relation)
        if err != nil {
            continue
        }

        options, err := json.Marshal(rec.Options)
        if err != nil {
            continue
        }
            
        _, err = db.client.Exec(
            sql, 
            rec.Id, 
            time.Now().UTC().Unix(),
            rec.LocalAddr.Name, 
            rec.LocalAddr.IP, 
            rec.RemoteAddr.Name, 
            rec.RemoteAddr.IP,
            relation, 
            options, 
        )

        if err != nil {
            return err
        }

        if _, ok := db.records.index[rec.LocalAddr.Name]; !ok {
            db.records.index[rec.LocalAddr.Name] = make(map[string]bool)
        }

        rec.Timestamp = time.Now().UTC().Unix()
        db.records.index[rec.LocalAddr.Name][rec.Id] = true
        db.records.items[rec.Id] = rec
        
    }

    return nil
}

func (db *Client) DelRecords(ids []string) error {
    db.records.Lock()
    defer db.records.Unlock()

    sql := "delete from records where id = ?"

    for _, id := range ids {
        _, err := db.client.Exec(sql, id)
        if err != nil { return err }

        rec, found := db.records.items[id]
        if !found { continue }

        if _, ok := db.records.index[rec.LocalAddr.Name]; ok {
            if _, ok := db.records.index[rec.LocalAddr.Name][id]; ok {
                delete(db.records.index[rec.LocalAddr.Name], id)
            }
            if len(db.records.index[rec.LocalAddr.Name]) == 0 {
                delete(db.records.index, rec.LocalAddr.Name)
            }
        }
    
        delete(db.records.items, id)
    }
    
    return nil
}

func (db *Client) LoadExceptions(args config.ExpArgs) ([]config.Exception, error) {
    db.exceptions.RLock()
    defer db.exceptions.RUnlock()

    var items []config.Exception

    if args.Id != "" {
        rec, found := db.exceptions.items[args.Id]
        if found {
            items = append(items, rec)
            return items, nil
        }
        return items, errors.New("object not found")
    }

    for _, val := range db.exceptions.items {
        if args.AccountID != "" {
            if fmt.Sprint(val.AccountID) != args.AccountID {
                continue
            }
        }

        items = append(items, val)
    }

    return items, nil
}

func (db *Client) SaveExceptions(records []config.Exception) error {
    db.exceptions.Lock()
    defer db.exceptions.Unlock()

    sql := "replace into exceptions (id,accountId,hostMask,ignoreMask) values (?,?,?,?)"

    for _, rec := range records {
        _, err := db.client.Exec(
            sql, 
            rec.Id, 
            rec.AccountID,
            rec.HostMask,
            rec.IgnoreMask,
        )

        if err != nil {
            return err
        }

        db.exceptions.items[rec.Id] = rec
    }
    
    return nil
}

func (db *Client) DelExceptions(ids []string) error {
    db.exceptions.Lock()
    defer db.exceptions.Unlock()

    sql := "delete from exceptions where id = ?"

    for _, id := range ids {
        _, err := db.client.Exec(sql, id)
        if err != nil { return err }

        delete(db.exceptions.items, id)
    }

    return nil
}