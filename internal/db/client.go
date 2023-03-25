package db

import (
    "errors"
    "github.com/ltkh/netmap/internal/config"
    "github.com/ltkh/netmap/internal/db/cache"
    "github.com/ltkh/netmap/internal/db/sqlite3"
    "github.com/ltkh/netmap/internal/db/cassandra"
)

type DbClient interface {
    CreateTables() error
    Close() error

	SaveStatus(records []config.SockTable) error
	SaveNetstat(records []config.SockTable) error

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
        //case "mysql":
        //    return mysql.NewClient(config)
        case "sqlite3":
            return sqlite3.NewClient(config)
        case "cache":
            return cache.NewClient(config)
        case "cassandra":
            return cassandra.NewClient(config)
    }
    return nil, errors.New("invalid client")
}