package couchdb

import (
    "fmt"
    "log"
    "time"
    //"regexp"
    "strings"
    //"encoding/json"
    //"database/sql"
    //
    //"context"
    "encoding/json"
	"github.com/ltkh/netmap/internal/client"
    "github.com/ltkh/netmap/internal/config"
)

type Client struct {
    client *client.HttpClient
}

type RecDocs struct {
    Docs           []Record               `json:"docs,omitempty"`
    Reason         string                 `json:"reason,omitempty"`
}

type Record struct {
    Id             string                 `json:"_id,omitempty"`
    Rev            string                 `json:"_rev,omitempty"`
    Deleted        bool                   `json:"_deleted,omitempty"`
    Record         config.SockTable       `json:"record,omitempty"`
    Error          string                 `json:"error,omitempty"`
    Reason         string                 `json:"reason,omitempty"`
}

type ExpDocs struct {
    Docs           []Exception            `json:"docs,omitempty"`
    Reason         string                 `json:"reason,omitempty"`
}

type Exception struct {
    Id             string                 `json:"_id,omitempty"`
    Rev            string                 `json:"_rev,omitempty"`
    Deleted        bool                   `json:"_deleted,omitempty"`
    Exception      config.Exception       `json:"record,omitempty"`
    Error          string                 `json:"error,omitempty"`
    Reason         string                 `json:"reason,omitempty"`
}

func NewClient(conf *config.DB) (*Client, error) {
    clt := client.NewHttpClient(&client.HttpConfig{
        URL: conf.ConnString,
        Username: conf.Username,
        Password: conf.Password,
    })

    return &Client{ client: clt }, nil
}

func (db *Client) Close() error {
    return nil
}

func (db *Client) CreateTables() error {
    bases := []string{"/records", "/exceptions"}

    for _, url := range bases {
        resp, err := db.client.NewRequest("PUT", url, nil)
        if err != nil {
            return err
        }
        if resp.StatusCode >= 400 {
            var rsp Record
            if err := json.Unmarshal(resp.Body, &rsp); err != nil {
                return err
            }
            log.Printf("[warn] couchdb: %v - %v", rsp.Reason, url)
        }
    }

    return nil
}

func (db *Client) GetRevision(id, base string) (string, error) {
    resp, err := db.client.NewRequest("HEAD", fmt.Sprintf("/%s/%s", base, id), nil)
    if err != nil {
        return "", err
    }
    rev := strings.Trim(resp.Header.Get("ETag"), "\"")
    if rev == "" {
        return "", fmt.Errorf("ETag header not found")
    }
    return rev, nil
}

func (db *Client) GetRecord(id string) (Record, error) {
    rec := Record{ Id: id }

    resp, err := db.client.NewRequest("GET", fmt.Sprintf("/records/%s", id), nil)
    if err != nil {
        return rec, err
    }

    if err := json.Unmarshal(resp.Body, &rec); err != nil {
        return rec, err
    }

    if resp.StatusCode >= 400 {
        return rec, fmt.Errorf(rec.Reason)
    }

    return rec, nil
}

func (db *Client) SaveStatus(records []config.SockTable) error {
    docs := RecDocs{}

    for _, rec := range records {
        record, err := db.GetRecord(rec.Id)
        if err != nil {
            log.Printf("[warn] couchbase: %v - /records/%v", err, rec.Id)
            continue
        }
        record.Record.Timestamp = time.Now().UTC().Unix()
        record.Record.Relation = rec.Relation
        docs.Docs = append(docs.Docs, record)
    }

    if len(docs.Docs) > 0 {
        jsonDocs, err := json.Marshal(docs)
        if err != nil {
            return err
        }

        _, err = db.client.NewRequest("POST", "/records/_bulk_docs", jsonDocs)
        if err != nil {
            return err
        }
    }

    return nil
}

func (db *Client) SaveNetstat(records []config.SockTable) error {
    docs := RecDocs{}

    for _, rec := range records {
        record, err := db.GetRecord(rec.Id)
        if err != nil {
            record.Record.LocalAddr = rec.LocalAddr
            record.Record.RemoteAddr = rec.RemoteAddr
            record.Record.Relation = rec.Relation
            record.Record.Options = rec.Options
        }
        record.Record.Timestamp = time.Now().UTC().Unix()
        docs.Docs = append(docs.Docs, record)
    }

    if len(docs.Docs) > 0 {
        jsonDocs, err := json.Marshal(docs)
        if err != nil {
            return err
        }

        _, err = db.client.NewRequest("POST", "/records/_bulk_docs", jsonDocs)
        if err != nil {
            return err
        }
    }

    return nil
}

func (db *Client) LoadRecords(args config.RecArgs) ([]config.SockTable, error) {
    result := []config.SockTable{}
    search := RecDocs{}
    swhere := []string{}

    if args.Id != "" {
        swhere = append(swhere, fmt.Sprintf("\"_id\":\"%s\"", args.Id)) 
    }

    if args.SrcName != "" {
        swhere = append(swhere, fmt.Sprintf("\"record.localAddr\":{\"name\":\"%s\"}", args.SrcName))
    }

    if args.Timestamp > 0 {
        swhere = append(swhere, fmt.Sprintf("\"record.timestamp\":{\"$gte\":%v}", args.Timestamp))
    }

    resp, err := db.client.NewRequest("POST", "/records/_find", []byte(fmt.Sprintf("{\"selector\":{%s}}", strings.Join(swhere, ","))))
    if err != nil {
        return result, err
    }

    if err := json.Unmarshal(resp.Body, &search); err != nil {
        return result, err
    }

    if resp.StatusCode >= 400 {
        return result, fmt.Errorf("couchdb: %v", search.Reason)
    }

    for _, rec := range search.Docs {
        rec.Record.Id = rec.Id
        result = append(result, rec.Record) 
    }

    return result, nil
}

func (db *Client) SaveRecords(records []config.SockTable) error {
    docs := RecDocs{}

    for _, rec := range records {
        revision, _ := db.GetRevision(rec.Id, "records")
        docs.Docs = append(docs.Docs, Record{
            Id: rec.Id,
            Rev: revision,
            Record: config.SockTable{
                Timestamp:  time.Now().UTC().Unix(),
                LocalAddr:  rec.LocalAddr,
                RemoteAddr: rec.RemoteAddr,
                Relation:   rec.Relation,
                Options:    rec.Options,
            },
        }) 
    }

    if len(docs.Docs) > 0 {
        jsonDocs, err := json.Marshal(docs)
        if err != nil {
            return err
        }

        _, err = db.client.NewRequest("POST", "/records/_bulk_docs", jsonDocs)
        if err != nil {
            return err
        }
    }

    return nil
}

func (db *Client) DelRecords(ids []string) error {
    docs := RecDocs{}

    for _, id := range ids {
        revision, err := db.GetRevision(id, "records")
        if err != nil {
            log.Printf("[warn] couchbase: %v - /records/%v", err, id)
            continue
        }

        docs.Docs = append(docs.Docs, Record{
            Id: id,
            Rev: revision,
            Deleted: true,
        })
    }

    if len(docs.Docs) > 0 {
        jsonDocs, err := json.Marshal(docs)
        if err != nil {
            return err
        }

        _, err = db.client.NewRequest("POST", "/records/_bulk_docs", jsonDocs)
        if err != nil {
            return err
        }
    }

    return nil
}

func (db *Client) LoadExceptions(args config.ExpArgs) ([]config.Exception, error) {
    result := []config.Exception{}
    search := ExpDocs{}
    swhere := []string{}

    if args.Id != "" {
        swhere = append(swhere, fmt.Sprintf("\"_id\":\"%s\"", args.Id))
    }

    if args.AccountID != "" {
        swhere = append(swhere, fmt.Sprintf("\"exception.accountID\":%s}", args.AccountID))
    }

    resp, err := db.client.NewRequest("POST", "/exceptions/_find", []byte(fmt.Sprintf("{\"selector\":{%s}}", strings.Join(swhere, ","))))
    if err != nil {
        return result, err
    }

    if err := json.Unmarshal(resp.Body, &search); err != nil {
        return result, err
    }

    if resp.StatusCode >= 400 {
        return result, fmt.Errorf("couchdb: %v", search.Reason)
    }

    for _, exp := range search.Docs {
        exp.Exception.Id = exp.Id
        result = append(result, exp.Exception) 
    }

    return result, nil
}

func (db *Client) SaveExceptions(records []config.Exception) error {
    docs := ExpDocs{}

    for _, exp := range records {
        revision, _ := db.GetRevision(exp.Id, "exceptions")
        docs.Docs = append(docs.Docs, Exception{
            Id: exp.Id,
            Rev: revision,
            Exception: config.Exception{
                AccountID:  exp.AccountID,
                HostMask:   exp.HostMask,
                IgnoreMask: exp.IgnoreMask,
            },
        }) 
    }

    if len(docs.Docs) > 0 {
        jsonDocs, err := json.Marshal(docs)
        if err != nil {
            return err
        }

        _, err = db.client.NewRequest("POST", "/exceptions/_bulk_docs", jsonDocs)
        if err != nil {
            return err
        }
    }
    
    return nil
}

func (db *Client) DelExceptions(ids []string) error {
    docs := ExpDocs{}

    for _, id := range ids {
        revision, err := db.GetRevision(id, "exceptions")
        if err != nil {
            log.Printf("[warn] couchbase: %v - /exceptions/%v", err, id)
            continue
        }

        docs.Docs = append(docs.Docs, Exception{
            Id: id,
            Rev: revision,
            Deleted: true,
        })
    }

    if len(docs.Docs) > 0 {
        jsonDocs, err := json.Marshal(docs)
        if err != nil {
            return err
        }

        _, err = db.client.NewRequest("POST", "/exceptions/_bulk_docs", jsonDocs)
        if err != nil {
            return err
        }
    }

    return nil
}