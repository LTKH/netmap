package mysql

import (
    //"os"
    "fmt"
    "log"
    "sync"
    "time"
    "errors"
    "encoding/json"
    "database/sql"
    msl "github.com/go-sql-driver/mysql"
    "github.com/ltkh/netmap/internal/config"
)

var (
    queue_limit = 1000000
)

type Client struct {
    records    Records 
    exceptions Exceptions
    queue      chan config.SockTable
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

    if conf.ConnString == "" {
        cfg := msl.Config{
            User:                 conf.Username,
            Passwd:               conf.Password,
            Net:                  "tcp",
            Addr:                 conf.Host,
            DBName:               conf.Name,
            AllowNativePasswords: true,
        }
        conf.ConnString = cfg.FormatDSN()
    }

    conn, err := sql.Open("mysql", conf.ConnString)
    if err != nil {
        return nil, err
    }

    // Set CacheLimit
    if conf.Limit == 0 {
        conf.Limit = 1000000
    }

    db := Client{
        records: Records{
            items: make(map[string]config.SockTable),
            index: make(map[string]map[string]bool),
        },
        exceptions: Exceptions{
            items: make(map[string]config.Exception),
        },
        queue: make(chan config.SockTable, queue_limit),
        client: conn, 
        config: conf,
    }

    go func(){
        for {
            rec := <- db.queue
            db.WriteRecord(rec)
        }
    }()

    return &db, nil
}

func (db *Client) Close() error {
    return nil
}

func (db *Client) CreateTables() error {
    _, err := db.client.Exec(fmt.Sprintf(`
      create table if not exists records (
        %[1]sid%[1]s            varchar(50) primary key,
        %[1]stimestamp%[1]s     bigint(20) default 0,
        %[1]slocalName%[1]s     varchar(50) not null,
        %[1]slocalIP%[1]s       varchar(20) not null,
        %[1]sremoteName%[1]s    varchar(50) not null,
        %[1]sremoteIP%[1]s      varchar(20) not null,
        %[1]srelation%[1]s      json,
        %[1]soptions%[1]s       json,
        unique key IDX_mon_records_id (id)
      ) engine InnoDB default charset=utf8mb4 collate=utf8mb4_unicode_ci;
    `, "`"))
    if err != nil {
        return err
    }

    _, err = db.client.Exec(fmt.Sprintf(`
      create table if not exists exceptions (
        %[1]sid%[1]s            varchar(50) primary key,
        %[1]saccountId%[1]s     int default 0,
        %[1]shostMask%[1]s      varchar(50) not null,
        %[1]signoreMask%[1]s    varchar(50) not null,
        unique key IDX_mon_exceptions_id (id)
      ) engine InnoDB default charset=utf8mb4 collate=utf8mb4_unicode_ci;
    `, "`"))
    if err != nil {
        return err
    }

    return nil
}

func (db *Client) LoadTableRecords() error {
    db.records.Lock()
    defer db.records.Unlock()

    sql := "select id,timestamp,localName,localIP,remoteName,remoteIP,relation,options from records order by id"

    rows, err := db.client.Query(sql)
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

        if _, ok := db.records.index[rec.LocalAddr.Name]; !ok {
            db.records.index[rec.LocalAddr.Name] = make(map[string]bool)
        }

        db.records.index[rec.LocalAddr.Name][rec.Id] = true
        //log.Printf("rec.LocalAddr.Name - %v", rec.LocalAddr.Name)
        db.records.items[rec.Id] = rec
    }

    return nil
}

func (db *Client) LoadTableExceptions() error {
    db.exceptions.Lock()
    defer db.exceptions.Unlock()

    sql := "select id,accountId,hostMask,ignoreMask from exceptions order by accountId,id"

    rows, err := db.client.Query(sql)
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

func (db *Client) SaveStatus(rec config.SockTable) error {
    db.records.Lock()
    defer db.records.Unlock()

    item, found := db.records.items[rec.Id]
    if !found {
        return nil
    }

    if item.Timestamp > rec.Timestamp {
        return nil
    }

    if item.Relation != rec.Relation {
        item.Relation = rec.Relation
        if len(db.queue) < queue_limit {
            db.queue <- item
        } else {
            log.Print("[error] DB write queue is full")
        }
    }

    db.records.items[rec.Id] = item

    return nil
}

func (db *Client) SaveNetstat(rec config.SockTable) error {
    return nil
}

func (db *Client) SaveTracert(rec config.SockTable) error {
    db.records.Lock()
    defer db.records.Unlock()

    item, found := db.records.items[rec.Id]
    if !found {
        return nil
    }

    if item.Timestamp > rec.Timestamp {
        return nil
    }

    item.Relation.Trace = 2

    if rec.Options.Command != "" {
        item.Options.Command = rec.Options.Command
        if len(db.queue) < queue_limit {
            db.queue <- item
        } else {
            log.Print("[error] DB write queue is full")
        }
    }
    
    db.records.items[rec.Id] = item

    return nil
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

func (db *Client) WriteRecord(rec config.SockTable) error {
    sql := "replace into records (id,timestamp,localName,localIP,remoteName,remoteIP,relation,options) values (?,?,?,?,?,?,?,?)"

    relation, err := json.Marshal(rec.Relation)
    if err != nil {
        return err
    }

    options, err := json.Marshal(rec.Options)
    if err != nil {
        return err
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

    return nil
}

func (db *Client) SaveRecord(rec config.SockTable) error {
    db.records.Lock()
    defer db.records.Unlock()

    if rec.Id == "" {
        rec.Id = config.GetIdRec(&rec)
    }

    item, found := db.records.items[rec.Id]
    if found && item.Timestamp > rec.Timestamp {
        return nil
    }

    if !found && len(db.records.items) >= db.config.Limit {
        return errors.New("cache limit exceeded")
    }

    if !found || (item.Relation != rec.Relation || item.Options != rec.Options) {
        if len(db.queue) < queue_limit {
            db.queue <- rec
        } else {
            log.Print("[error] DB write queue is full")
        }
    }

    if _, ok := db.records.index[rec.LocalAddr.Name]; !ok {
        db.records.index[rec.LocalAddr.Name] = make(map[string]bool)
    }

    db.records.index[rec.LocalAddr.Name][rec.Id] = true
    db.records.items[rec.Id] = rec

    return nil
}

func (db *Client) DelRecord(id string) error {
    db.records.Lock()
    defer db.records.Unlock()

    _, err := db.client.Exec("delete from records where id = ?", id)
    if err != nil { return err }

    rec, found := db.records.items[id]
    if !found { return nil }

    if _, ok := db.records.index[rec.LocalAddr.Name]; ok {
        if _, ok := db.records.index[rec.LocalAddr.Name][id]; ok {
            delete(db.records.index[rec.LocalAddr.Name], id)
        }
        if len(db.records.index[rec.LocalAddr.Name]) == 0 {
            delete(db.records.index, rec.LocalAddr.Name)
        }
    }

    delete(db.records.items, id)
    
    return nil
}

func (db *Client) LoadExceptions(args config.ExpArgs) ([]config.Exception, error) {
    db.exceptions.RLock()
    defer db.exceptions.RUnlock()

    items := []config.Exception{}

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

func (db *Client) SaveException(rec config.SockTable) error {
    db.exceptions.Lock()
    defer db.exceptions.Unlock()

    except, found := db.records.items[rec.Id]
    if found && except.Timestamp > rec.Timestamp {
        return nil
    }

    _, err := db.client.Exec(
        "replace into exceptions (id,accountId,hostMask,ignoreMask) values (?,?,?,?)", 
        rec.Id, 
        rec.Options.AccountID,
        rec.Options.HostMask,
        rec.Options.IgnoreMask,
    )

    if err != nil {
        return err
    }

    db.exceptions.items[rec.Id] = config.Exception{
        Id:            rec.Id,
        Timestamp:     rec.Timestamp,
        AccountID:     rec.Options.AccountID,
        HostMask:      rec.Options.HostMask,
        IgnoreMask:    rec.Options.IgnoreMask,
    }
    
    return nil
}

func (db *Client) DelException(id string) error {
    db.exceptions.Lock()
    defer db.exceptions.Unlock()

    _, err := db.client.Exec("delete from exceptions where id = ?", id)
    if err != nil { return err }

    delete(db.exceptions.items, id)

    return nil
}