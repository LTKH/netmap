package db

import (
    "errors"
    "github.com/ltkh/netmap/internal/config"
    "github.com/ltkh/netmap/internal/db/cache"
    "github.com/ltkh/netmap/internal/db/mysql"
    "github.com/ltkh/netmap/internal/db/sqlite3"
)

type DbClient interface {
    CreateTables() error
    LoadTables() error
    Close() error

    SaveStatus(rec config.SockTable) error
    SaveNetstat(rec config.SockTable) error
    SaveTracert(rec config.SockTable) error

    LoadRecords(args config.RecArgs) ([]config.SockTable, error)
    SaveRecord(rec config.SockTable) error
    DelRecord(id string) error

    LoadExceptions(args config.ExpArgs) ([]config.Exception, error)
    SaveException(rec config.SockTable) error
    DelException(id string) error
}

func NewClient(config *config.DB) (DbClient, error) {
    switch config.Client {
    case "cache":
        return cache.New(config)
    case "mysql":
        return mysql.New(config)
    case "sqlite3":
        return sqlite3.New(config)
    }
    return nil, errors.New("invalid client")
}