package redis

import (
    //"fmt"
    "log"
    //"time"
	//"context"
    //"regexp"
    //"strings"
    //"encoding/json"
    //"database/sql"
    //
    //"context"
    //"encoding/json"
	"github.com/gomodule/redigo/redis"
	//goredis "github.com/redis/go-redis/v9"
	"github.com/nitishm/go-rejson/v4"
	//"github.com/ltkh/netmap/internal/client"
    "github.com/ltkh/netmap/internal/config"
)

type Client struct {
    client redis.Conn
    config *config.DB
	rh *rejson.Handler
}

type Records struct {
    Records     config.SockTable
}

type Exceptions struct {
    Exceptions  config.Exception
}

func New(conf *config.DB) (*Client, error) {
	conn, err := redis.Dial("tcp", conf.ConnString)
    if err != nil {
        return &Client{}, err
    }
	//cli := redis.NewClient(&redis.Options{
    //    Addr:     conf.ConnString,
    //    Password: conf.Password,
    //    DB:       0,
    //})
	//rh := rejson.NewReJSONHandler()
	//rh.SetGoRedisClient(cli)
    //return &Client{ client: cli, config: conf, rh: rh }, nil
	rh := rejson.NewReJSONHandler()
	rh.SetRedigoClient(conn)
    return &Client{ client: conn, config: conf, rh: rh }, nil
}

func (db *Client) Close() error {
    return db.client.Close()
}

func (db *Client) CreateTables() error {
    return nil
}

func (db *Client) LoadTables() error {
    return nil
}

func (db *Client) SaveStatus(rec config.SockTable) error {
    return nil
}

func (db *Client) SaveNetstat(rec config.SockTable) error {
    return nil
}

func (db *Client) SaveTracert(rec config.SockTable) error {
    return nil
}

func (db *Client) LoadRecords(args config.RecArgs) ([]config.SockTable, error) {
    result := []config.SockTable{}
    return result, nil
}

func (db *Client) SaveRecord(rec config.SockTable) error {
    _, err := db.rh.JSONSet("record:"+rec.Id, ".", rec)
    if err != nil {
        log.Printf("failed to JSONSet: %v", err)
        return nil
    }

    return nil
}

func (db *Client) DelRecord(id string) error {
    return nil
}

func (db *Client) LoadExceptions(args config.ExpArgs) ([]config.Exception, error) {
    result := []config.Exception{}
    return result, nil
}

func (db *Client) SaveException(rec config.SockTable) error {
    return nil
}

func (db *Client) DelException(id string) error {
    return nil
}