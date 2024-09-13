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

    SaveStatus(records []config.SockTable) error
    SaveNetstat(records []config.SockTable) error
    SaveTracert(records []config.SockTable) error

    LoadRecords(args config.RecArgs) ([]config.SockTable, error)
    SaveRecords(records []config.SockTable) error
    DelRecords(ids []string) error

    LoadExceptions(args config.ExpArgs) ([]config.Exception, error)
    SaveExceptions(records []config.Exception) error
    DelExceptions(ids []string) error
    
    //Healthy() error
    //LoadUser(login string) (cache.User, error)
    //SaveUser(user cache.User) error
    //LoadUsers() ([]cache.User, error)
    //LoadAlerts() ([]cache.Alert, error)
    //SaveAlerts(alerts map[string]cache.Alert) error
    //AddAlert(alert cache.Alert) error
    //UpdAlert(alert cache.Alert) error
    //DeleteOldAlerts() (int64, error)
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