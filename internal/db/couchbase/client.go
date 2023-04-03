package couchbase

import (
    //"log"
    "fmt"
    "time"
    "regexp"
    "strings"
    //"encoding/json"
    //"database/sql"
    "github.com/couchbase/gocb/v2"
    "github.com/ltkh/netmap/internal/config"
)

type Client struct {
    cluster     *gocb.Cluster
    scope       *gocb.Scope
    bucket      string
}

type Records struct {
    Records     config.SockTable
}

type Exceptions struct {
    Exceptions  config.Exception
}

func NewClient(conf *config.DB) (*Client, error) {
    if conf.Bucket == "" {
        conf.Bucket = "netmap"
    }

    // Update this to your cluster details
    options := gocb.ClusterOptions{
        Authenticator: gocb.PasswordAuthenticator{
            Username: conf.Username,
            Password: conf.Password,
        },
        TimeoutsConfig: gocb.TimeoutsConfig{
            ConnectTimeout: 5 * time.Second,
            QueryTimeout:   60 * time.Second,
            SearchTimeout:  60 * time.Second,
        },
    }

    // Initialize the Connection
    cluster, err := gocb.Connect(conf.ConnString, options)
    if err != nil {
        return nil, err
    }

    // For Server versions 6.5 or later you do not need to open a bucket here
    bucket := cluster.Bucket(conf.Bucket)

    // We wait until the bucket is definitely connected and setup
    err = bucket.WaitUntilReady(5*time.Second, nil)
    if err != nil {
        return nil, err
    }

    return &Client{ 
        cluster: cluster, 
        scope: cluster.Bucket(conf.Bucket).Scope("v1"), 
        bucket: conf.Bucket,
    }, nil
}

func (db *Client) Close() error {

    return nil
}

func (db *Client) CreateTables() error {
    var err error

    _, err = db.cluster.Query(
        fmt.Sprintf("CREATE SCOPE `%s`.v1 IF NOT EXISTS", db.bucket),
        &gocb.QueryOptions{},
    )
    if err != nil {
       return err
    }
    _, err = db.cluster.Query(
        fmt.Sprintf("CREATE COLLECTION `%s`.v1.records IF NOT EXISTS", db.bucket),
        &gocb.QueryOptions{},
    )
    if err != nil {
       return err
    }
    _, err = db.cluster.Query(
        fmt.Sprintf("CREATE COLLECTION `%s`.v1.exceptions IF NOT EXISTS", db.bucket),
        &gocb.QueryOptions{},
    )
    if err != nil {
       return err
    }
    _, err = db.cluster.Query(
        fmt.Sprintf("CREATE PRIMARY INDEX `#primary` IF NOT EXISTS ON `%s`.v1.records", db.bucket),
        &gocb.QueryOptions{},
    )
    if err != nil {
       return err
    }
    _, err = db.cluster.Query(
        fmt.Sprintf("CREATE PRIMARY INDEX `#primary` IF NOT EXISTS ON `%s`.v1.exceptions", db.bucket),
        &gocb.QueryOptions{},
    )
    if err != nil {
       return err
    }

    return nil
}

func (db *Client) SaveStatus(records []config.SockTable) error {

    collection := db.scope.Collection("records")

    for _, rec := range records {
        relation := []gocb.MutateInSpec{
            gocb.UpsertSpec("relation", rec.Relation, &gocb.UpsertSpecOptions{}),
            gocb.UpsertSpec("timestamp", time.Now().UTC().Unix(), &gocb.UpsertSpecOptions{}),
        }
        _, err := collection.MutateIn(rec.Id, relation, &gocb.MutateInOptions{})
        if err != nil {
            return err
        }
    }

    return nil
}

func (db *Client) SaveNetstat(records []config.SockTable) error {
    
    collection := db.scope.Collection("records")

    for _, rec := range records {
        rec.Timestamp = time.Now().UTC().Unix()
        _, err := collection.Insert(rec.Id, rec, nil)
        if err != nil {
            timestamp := []gocb.MutateInSpec{
                gocb.UpsertSpec("timestamp", rec.Timestamp, &gocb.UpsertSpecOptions{}),
            }
            _, err := collection.MutateIn(rec.Id, timestamp, &gocb.MutateInOptions{})
            if err != nil {
                return err
            }
        }
    }

    return nil
}

func (db *Client) LoadRecords(args config.RecArgs) ([]config.SockTable, error) {
    result := []config.SockTable{}

    sql := "SELECT * FROM records ORDER BY id"

    swhere := []string{}
    params := map[string]interface{}{}
    if args.Id != "" {
        swhere = append(swhere, "id = $id")
        params["id"] = args.Id
    }
    if args.SrcName != "" {
        swhere = append(swhere, "localAddr.name = $src_name")
        params["src_name"] = args.SrcName
    }
    if args.Timestamp > 0 {
        swhere = append(swhere, "timestamp >= $timestamp")
        params["timestamp"] = args.Timestamp
    }

    if len(swhere) > 0 {
        sql = fmt.Sprintf("SELECT * FROM records WHERE %v ORDER BY id", strings.Join(swhere, " AND "))
    } 

    queryResult, err := db.scope.Query(sql, &gocb.QueryOptions{ NamedParameters: params, Adhoc: true })
    if err != nil {
        return result, err
    }

    // Print each found Row
    for queryResult.Next() {
        var rec Records
        //var rec config.SockTable
        err := queryResult.Row(&rec)
        if err != nil {
            return result, err
        }
        result = append(result, rec.Records)
    }

    if err := queryResult.Err(); err != nil {
        return result, err
    }
 
    return result, nil
}

func (db *Client) SaveRecords(records []config.SockTable) error {

    collection := db.scope.Collection("records")

    for _, rec := range records {
        rec.Timestamp = time.Now().UTC().Unix()
        _, err := collection.Upsert(rec.Id, rec, nil)
        if err != nil {
            return err
        }
    }

    return nil
}

func (db *Client) DelRecords(ids []string) error {

    collection := db.scope.Collection("records")

    for _, id := range ids {
        _, err := collection.Remove(id, &gocb.RemoveOptions{})
        if err != nil {
            return err
        }
    }

    return nil
}

func (db *Client) LoadExceptions(args config.ExpArgs) ([]config.Exception, error) {
    result := []config.Exception{}

    sql := "SELECT * FROM exceptions ORDER BY id"

    swhere := []string{}
    params := map[string]interface{}{}
    if args.Id != "" {
        swhere = append(swhere, "id = $id")
        params["id"] = args.Id
    }
    if args.AccountID != 0 {
        swhere = append(swhere, "accountId = $accountId")
        params["accountId"] = args.AccountID
    }

    if len(swhere) > 0 {
        sql = fmt.Sprintf("SELECT * FROM exceptions WHERE %v ORDER BY id", strings.Join(swhere, " AND "))
    }

    queryResult, err := db.scope.Query(sql, &gocb.QueryOptions{})
    if err != nil {
        return result, err
    }

    for queryResult.Next() {
        var exp Exceptions
        err := queryResult.Row(&exp)
        if err != nil {
            return result, err
        }
        
        if args.SrcName != "" {
            matched, _ := regexp.MatchString(exp.Exceptions.HostMask, args.SrcName)
            if !matched {
                continue
            } 
        }
        result = append(result, exp.Exceptions)
    }

    if err := queryResult.Err(); err != nil {
        return result, err
    }

    return result, nil
}

func (db *Client) SaveExceptions(records []config.Exception) error {

    collection := db.scope.Collection("exceptions")

    for _, exp := range records {
        _, err := collection.Upsert(exp.Id, exp, nil)
        if err != nil {
            return err
        }
    }
    
    return nil
}

func (db *Client) DelExceptions(ids []string) error {

    collection := db.scope.Collection("exceptions")

    for _, id := range ids {
        _, err := collection.Remove(id, &gocb.RemoveOptions{})
        if err != nil {
            return err
        }
    }

    return nil
}