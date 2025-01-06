package v1

import (
    //"log"
    //"fmt"
    //"strconv"
    //"net/http"
    //"time"
    //"errors"
    //"compress/gzip"
    //"io"
    //"bytes"
    //"regexp"
    //"io/ioutil"
    //"encoding/json"
    //"github.com/prometheus/client_golang/prometheus"
    "github.com/ltkh/netmap/internal/config"
    //"github.com/ltkh/netmap/internal/client"
    "github.com/ltkh/netmap/internal/db"
)

type RPC struct{
    DB *db.DbClient
}

func NewRPC(conf *config.Config, db db.DbClient) (*RPC, error) {
    if err := db.CreateTables(); err != nil {
        return &RPC{}, err
    }
    if err := db.LoadTables(); err != nil {
        return &RPC{}, err
    }
    return &RPC{DB: &db}, nil
}

func (rpc *RPC) SetStatus(items []config.SockTable, reply *string) error {
    err := db.DbClient.SaveStatus(*rpc.DB, items)
    return err
}

func (rpc *RPC) SetNetstat(items []config.SockTable, reply *string) error {
    err := db.DbClient.SaveNetstat(*rpc.DB, items)
    return err
}

func (rpc *RPC) SetTracert(items []config.SockTable, reply *string) error {
    err := db.DbClient.SaveTracert(*rpc.DB, items)
    return err
}

func (rpc *RPC) GetRecords(args config.RecArgs, items *[]config.SockTable) error {
    var err error
    *items, err = db.DbClient.LoadRecords(*rpc.DB, args)
    return err
}

func (rpc *RPC) SetRecords(items []config.SockTable, reply *string) error {
    err := db.DbClient.SaveRecords(*rpc.DB, items)
    return err
}

func (rpc *RPC) DelRecords(ids []string, reply *string) error {
    err := db.DbClient.DelRecords(*rpc.DB, ids)
    return err
}

func (rpc *RPC) GetExceptions(args config.ExpArgs, items *[]config.Exception) error {
    var err error
    *items, err = db.DbClient.LoadExceptions(*rpc.DB, args)
    return err
}

func (rpc *RPC) SetExceptions(items []config.Exception, reply *string) error {
    err := db.DbClient.SaveExceptions(*rpc.DB, items)
    return err
}

func (rpc *RPC) DelExceptions(ids []string, reply *string) error {
    err := db.DbClient.DelExceptions(*rpc.DB, ids)
    return err
}
