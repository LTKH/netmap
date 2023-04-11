package sqlite3

import (
    "fmt"
    //"log"
    "time"
    "regexp"
    "strings"
    "encoding/json"
    "database/sql"
    _ "github.com/mattn/go-sqlite3"
    "github.com/ltkh/netmap/internal/config"
)

type Client struct {
    client *sql.DB
    config *config.DB
}

func NewClient(conf *config.DB) (*Client, error) {
    conn, err := sql.Open("sqlite3", conf.ConnString)
    if err != nil {
        return nil, err
    }
    return &Client{ client: conn, config: conf }, nil
}

func (db *Client) Close() error {
    db.client.Close()

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

func (db *Client) SaveStatus(records []config.SockTable) error {
    sql := "update records set timestamp = ?, relation = ? where id = ?"

    for _, rec := range records {

        relation, err := json.Marshal(rec.Relation)
        if err != nil {
            return err
            continue
        }

        if rec.Id == "" {
            continue
        }
        
        _, err = db.client.Exec(
            sql, 
            time.Now().UTC().Unix(),
            relation, 
            rec.Id,
        )
        if err != nil {
            return err
        }
    }

    return nil
}

func (db *Client) SaveNetstat(records []config.SockTable) error {

    for _, rec := range records {

        relation, err := json.Marshal(rec.Relation)
        if err != nil {
            continue
        }

        options, err := json.Marshal(rec.Options)
        if err != nil {
            continue
        }

        if rec.Id == "" {
            continue
        }
        
        _, err = db.client.Exec(
            "insert into records (id,timestamp,localName,localIP,remoteName,remoteIP,relation,options) values (?,?,?,?,?,?,?,?)", 
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
            db.client.Exec(
                "update records set timestamp = ? where id = ?", 
                time.Now().UTC().Unix(), 
                rec.Id,
            )
            continue
        }
    }

    return nil
}

func (db *Client) LoadRecords(args config.RecArgs) ([]config.SockTable, error) {
    result := []config.SockTable{}
    swhere := []string{}
    awhere := []interface{}{}

    sql := "select id,timestamp,localName,localIP,remoteName,remoteIP,relation,options from records order by id"

    if args.Id != "" {
        swhere = append(swhere, "id = ?")
        awhere = append(awhere, args.Id)
    }
    if args.SrcName != "" {
        swhere = append(swhere, "localName = ?")
        awhere = append(awhere, args.SrcName)
    }
    if args.Timestamp > 0 {
        swhere = append(swhere, "timestamp >= ?")
        awhere = append(awhere, args.Timestamp)
    }

    if len(swhere) > 0 {
        sql = fmt.Sprintf("select id,timestamp,localName,localIP,remoteName,remoteIP,relation,options from records where %v order by id", strings.Join(swhere, " AND "))
    }

    rows, err := db.client.Query(sql, awhere...)
    if err != nil {
        return nil, err
    }
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
        if err != nil { return nil, err }
        err = json.Unmarshal(relation, &rec.Relation)
        if err != nil { continue }
        err = json.Unmarshal(options, &rec.Options)
        if err != nil { continue }
        result = append(result, rec) 
    }

    return result, nil
}

func (db *Client) SaveRecords(records []config.SockTable) error {
    sql := "replace into records (id,timestamp,localName,localIP,remoteName,remoteIP,relation,options) values (?,?,?,?,?,?,?,?)"

    for _, rec := range records {

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
    }

    return nil
}

func (db *Client) DelRecords(ids []string) error {
    sids := []string{}
    aids := []interface{}{}

    for _, id := range ids {
        sids = append(sids, "?") 
        aids = append(aids, id) 
    }

    _, err := db.client.Exec(fmt.Sprintf("delete from records where id in (%v)", strings.Join(sids, ",")), aids...)
    if err != nil {
        return err
    }

    return nil
}

func (db *Client) LoadExceptions(args config.ExpArgs) ([]config.Exception, error) {
    result := []config.Exception{}
    swhere := []string{}
    awhere := []interface{}{}

    sql := "select id,accountId,hostMask,ignoreMask from exceptions order by accountId,id"

    if args.Id != "" {
        swhere = append(swhere, "id = ?")
        awhere = append(awhere, args.Id)
    }
    if args.AccountID != 0 {
        swhere = append(swhere, "accountId = ?")
        awhere = append(awhere, args.AccountID)
    }

    if len(swhere) > 0 {
        sql = fmt.Sprintf("select id,accountId,hostMask,ignoreMask from exceptions where %v order by accountId,id", strings.Join(swhere, " AND "))
    }

    rows, err := db.client.Query(sql, awhere...)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    for rows.Next() {
        var exp config.Exception
        err := rows.Scan(
            &exp.Id, 
            &exp.AccountID,
            &exp.HostMask,
            &exp.IgnoreMask,
        )
        if err != nil { 
            return nil, err 
        }

        if args.SrcName != "" {
            matched, _ := regexp.MatchString(exp.HostMask, args.SrcName)
            if !matched {
                continue
            } 
        }
        result = append(result, exp) 
    }

    return result, nil
}

func (db *Client) SaveExceptions(records []config.Exception) error {
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
    }
    
    return nil
}

func (db *Client) DelExceptions(ids []string) error {
    sids := []string{}
    aids := []interface{}{}

    for _, id := range ids {
        sids = append(sids, "?") 
        aids = append(aids, id) 
    }

    _, err := db.client.Exec(fmt.Sprintf("delete from exceptions where id in (%v)", strings.Join(sids, ",")), aids...)
    if err != nil {
        return err
    }

    return nil
}