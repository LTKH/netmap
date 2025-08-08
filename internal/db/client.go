package db

import (
    "errors"
    "github.com/ltkh/netmap/internal/config"
    "github.com/ltkh/netmap/internal/db/cache"
    "github.com/ltkh/netmap/internal/db/sqlite3"
    "github.com/ltkh/netmap/internal/db/redis"
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

    LoadExceptions(args config.ExpArgs) ([]interface{}, error)
    SaveException(rec config.SockTable) error
    DelException(id string) error
}

func NewClient(config *config.DB) (DbClient, error) {
    switch config.Client {
        case "sqlite3":
            return sqlite3.New(config)
        case "cache":
            return cache.New(config)
        case "redis":
            return redis.New(config)
    }
    return nil, errors.New("invalid client")
}