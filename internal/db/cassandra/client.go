package cassandra

import (
    //"fmt"
    "time"
    //"regexp"
    "strings"
    "encoding/json"
    //"database/sql"
	"github.com/gocql/gocql"
    "github.com/ltkh/netmap/internal/config"
)

type Client struct {
    cluster *gocql.ClusterConfig
    session *gocql.Session
}

func NewClient(conf *config.DB) (*Client, error) {
    cluster := gocql.NewCluster(strings.Split(conf.ConnString, ",")...)
    cluster.Consistency = gocql.Quorum
    cluster.ProtoVersion = 4
    cluster.ConnectTimeout = time.Second * 30
    cluster.Authenticator = gocql.PasswordAuthenticator{Username: conf.Username, Password: conf.Password}
    session, err := cluster.CreateSession()
    if err != nil {
        return nil, err
    }

	return &Client{ cluster: cluster, session: session }, nil
}

func (db *Client) Close() error {
    db.session.Close()

    return nil
}

func (db *Client) CreateTables() error {
    err := db.session.Query(`
        CREATE  KEYSPACE IF NOT EXISTS netmap
        WITH REPLICATION = { 
            'class' : 'SimpleStrategy', 
            'replication_factor' : 1 
        };`).Exec()
    if err != nil {
        return err
    }

    db.session.Query(`DROP TABLE netmap.records;`).Exec()

    err = db.session.Query(`CREATE type netmap.relation_type ( 
            mode      varchar, 
            port      int, 
            command   text,
            result    int,
            response  float,
            trace     int 
        );`).Exec()
    if err != nil {
        return err
    }

    err = db.session.Query(`CREATE type netmap.options_type ( 
            service     varchar, 
            status      varchar, 
            command     text,
            timeout     float,
            maxRespTime float,
            accountID   int
        );`).Exec()
    if err != nil {
        return err
    }

    err = db.session.Query(`
        CREATE TABLE IF NOT EXISTS netmap.records (
            id            varchar PRIMARY KEY,
            type          varchar,
            time          bigint,
            localName     varchar,
            localIP       varchar,
            remoteName    varchar,
            remoteIP      varchar,
            relation      relation_type,
            options       options_type
        );`).Exec()
    if err != nil {
        return err
    }

    err = db.session.Query(`
        CREATE TABLE IF NOT EXISTS netmap.exceptions (
            id            varchar PRIMARY KEY,
            accountId     int,
            hostMask      varchar,
            ignoreMask    varchar
        );`).Exec()
    if err != nil {
        return err
    }

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
    //sleep_time_output := db.session.Query("SELECT avg(sleep_time_hours) FROM sleep_centre.sleep_study WHERE name = 'James';").Iter()
    //sleep_time_output.Scan(&sleep_time_hours)
    //iter := session.Query("SELECT * FROM book.book").Iter()
    //var book Book
    //for iter.Scan(&book.Title ,&book.Amount ,&book.CreatedOn,&book.Available{
    //       fmt.Println(book.Title , book.Amount,book.CreatedO,book.Available)
    //       j, ERR:= json.Marshal(&iter)
    //       if ERR != nil {panic(ERR)}
    //       //do things with j 
    //}
    return result, nil
}

func (db *Client) SaveRecords(records []config.SockTable) error {
    for _, rec := range records {

        /*
        relation, err := json.Marshal(rec.Relation)
        if err != nil {
            continue
        }

        options, err := json.Marshal(rec.Options)
        if err != nil {
            continue
        }
        */

        record, err := json.Marshal(rec)
        if err != nil {
            continue
        }

        //,fromJson(?),fromJson(?)

        err = db.session.Query("INSERT INTO netmap.records JSON ?;", string(record)).Exec()
        if err != nil {
            return err
        }
    }

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