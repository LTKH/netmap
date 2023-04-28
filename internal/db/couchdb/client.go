package couchdb

import (
    //"fmt"
    //"log"
    //"time"
    //"regexp"
    //"strings"
    //"encoding/json"
    //"database/sql"
    //
    //"context"
    "github.com/ltkh/netmap/internal/config"
)

type Client struct {
}

func NewClient(conf *config.DB) (*Client, error) {



    return &Client{ 
        
    }, nil
}

func (db *Client) Close() error {
    //db.conn.Close()

    return nil
}

func (db *Client) CreateTables() error {

    return nil
}

func (db *Client) SaveStatus(records []config.SockTable) error {

    return nil
}

func (db *Client) SaveNetstat(records []config.SockTable) error {

    return nil
}

func (db *Client) LoadRecords(args config.RecArgs) ([]config.SockTable, error) {
    result := []config.SockTable{}

    return result, nil
}

func (db *Client) SaveRecords(records []config.SockTable) error {

    return nil
}

func (db *Client) DelRecords(ids []string) error {

    return nil
}

func (db *Client) LoadExceptions(args config.ExpArgs) ([]config.Exception, error) {
    result := []config.Exception{}

    return result, nil
}

func (db *Client) SaveExceptions(records []config.Exception) error {
    
    return nil
}

func (db *Client) DelExceptions(ids []string) error {

    return nil
}