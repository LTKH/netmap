package sqlite3

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ltkh/netmap/internal/config"
	_ "github.com/mattn/go-sqlite3"
)

var (
	queue_limit = 1000000
)

type Client struct {
	records    Records
	exceptions Exceptions
	queue      chan config.SockTable
	client     *sql.DB
	config     *config.DB
}

type Records struct {
	sync.RWMutex
	items map[string]config.SockTable
	index map[string]map[string]bool
}

type Exceptions struct {
	sync.RWMutex
	items map[string]config.Exception
}

func New(conf *config.DB) (*Client, error) {

	if _, err := os.Stat(conf.ConnString); errors.Is(err, os.ErrNotExist) {
		_, err := os.Create(conf.ConnString)
		if err != nil {
			return nil, err
		}
	}
	conn, err := sql.Open("sqlite3", conf.ConnString)
	if err != nil {
		return nil, err
	}

	if conf.Limit == 0 {
		conf.Limit = 1000000
	}

	db := Client{
		records: Records{
			items: make(map[string]config.SockTable),
			index: make(map[string]map[string]bool),
		},
		exceptions: Exceptions{
			items: make(map[string]config.Exception),
		},
		queue:  make(chan config.SockTable, queue_limit),
		client: conn,
		config: conf,
	}

	go func() {
		for {
			rec := <-db.queue
			db.WriteRecord(rec)
		}
	}()

	return &db, nil
}

func (db *Client) Close() error {
	return db.client.Close()
}

func (db *Client) CreateTables() error {
	_, err := db.client.Exec(
		`create table if not exists records (
        id            varchar(50) primary key,
        timestamp     bigint(20) default 0,
        localName     varchar(50) not null,
        localIP       varchar(20) not null,
        remoteName    varchar(50) not null,
        remoteIP      varchar(20) not null,
        relation      json,
        options       json
      );
      create index if not exists localNameIdx 
        ON records (localName);
      create index if not exists remoteNameIdx 
        ON records (remoteName);
      create index if not exists statusIdx 
        ON records (json_extract(options, '$.status'));
      create index if not exists pingIdx 
        ON records (json_extract(relation, '$.ping'));
      create index if not exists srcInfoIdx 
        ON records (json_extract(options, '$.src_info'));
      create index if not exists dstInfoIdx 
        ON records (json_extract(options, '$.dst_info'));
      create table if not exists exceptions (
        id            varchar(50) primary key,
        accountId     int default 0,
        hostMask      varchar(50) not null,
        ignoreMask    varchar(50) not null
      );`)
	if err != nil {
		return err
	}

	return nil
}

func (db *Client) LoadTableRecords() error {
	db.records.Lock()
	defer db.records.Unlock()

	sql := "select id,timestamp,localName,localIP,remoteName,remoteIP,relation,options from records order by id"

	rows, err := db.client.Query(sql)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var rec config.SockTable
		var relation []uint8
		var options []uint8
		var LocalIP []byte
		var RemoteIP []byte

		err := rows.Scan(
			&rec.Id,
			&rec.Timestamp,
			&rec.LocalAddr.Name,
			&LocalIP,
			&rec.RemoteAddr.Name,
			&RemoteIP,
			&relation,
			&options,
		)
		if err != nil {
			return err
		}

		if len(LocalIP) > 0 {
			rec.LocalAddr.IP = net.IP(LocalIP).String()
		}
		if len(RemoteIP) > 0 {
			rec.RemoteAddr.IP = net.IP(RemoteIP).String()
		}

		err = json.Unmarshal(relation, &rec.Relation)
		if err != nil {
			continue
		}
		err = json.Unmarshal(options, &rec.Options)
		if err != nil {
			continue
		}

		if _, ok := db.records.index[rec.LocalAddr.Name]; !ok {
			db.records.index[rec.LocalAddr.Name] = make(map[string]bool)
		}

		db.records.index[rec.LocalAddr.Name][rec.Id] = true
		db.records.items[rec.Id] = rec
	}

	return nil
}

func (db *Client) LoadTableExceptions() error {
	db.exceptions.Lock()
	defer db.exceptions.Unlock()

	sql := "select id,accountId,hostMask,ignoreMask from exceptions order by accountId,id"

	rows, err := db.client.Query(sql)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var exp config.Exception
		err := rows.Scan(
			&exp.Id,
			&exp.AccountID,
			&exp.HostMask,
			&exp.IgnoreMask,
		)
		if err != nil {
			return err
		}
		db.exceptions.items[exp.Id] = exp
	}

	return nil
}

func (db *Client) LoadTables() error {

	if err := db.LoadTableRecords(); err != nil {
		return err
	}

	if err := db.LoadTableExceptions(); err != nil {
		return err
	}

	return nil
}

func (db *Client) SaveStatus(rec config.SockTable) error {
	db.records.Lock()
	defer db.records.Unlock()

	item, found := db.records.items[rec.Id]
	if !found {
		return nil
	}

	if item.Timestamp > rec.Timestamp {
		return nil
	}

	if item.Relation != rec.Relation {
		item.Relation = rec.Relation
		if len(db.queue) < queue_limit {
			db.queue <- item
		} else {
			log.Print("[error] DB write queue is full")
		}
	}

	db.records.items[rec.Id] = item

	return nil
}

func (db *Client) SaveNetstat(rec config.SockTable) error {
	return nil
}

func (db *Client) SaveTracert(rec config.SockTable) error {
	db.records.Lock()
	defer db.records.Unlock()

	item, found := db.records.items[rec.Id]
	if !found {
		return nil
	}

	if item.Timestamp > rec.Timestamp {
		return nil
	}

	item.Relation.Trace = 2

	if rec.Options.Command != "" {
		item.Options.Command = rec.Options.Command
		if len(db.queue) < queue_limit {
			db.queue <- item
		} else {
			log.Print("[error] DB write queue is full")
		}
	}

	db.records.items[rec.Id] = item

	return nil
}

func (db *Client) LoadRecords(args config.RecArgs) ([]config.SockTable, error) {
	db.records.RLock()
	defer db.records.RUnlock()

	var items []config.SockTable

	hasFilters := args.Id != "" || args.Type != "" || args.Timestamp != "" ||
		args.RelationPort != "" || args.RelationType != "" || args.RelationMode != "" ||
		args.RelationResult != "" || args.RelationTrace != "" ||
		args.OptionsService != "" || args.OptionsStatus != "" || args.OptionsAccountId != "" ||
		args.LocalAddrName != "" || args.LocalAddrIp != "" || args.LocalAddrPort != "" ||
		args.RemoteAddrName != "" || args.RemoteAddrIp != "" || args.RemoteAddrPort != "" ||
		args.OptionsSrcInfo != "" || args.OptionsDstInfo != "" || args.OptionsDescriptions != "" ||
		args.RelationPing != "" || args.RelationPacketLoss != "" ||
		args.RelationMinRtt != "" || args.RelationMaxRtt != "" || args.RelationAvgRtt != ""

	if !hasFilters && args.SrcName == "" {
		for _, val := range db.records.items {
			items = append(items, val)
		}
		return items, nil
	}

	var recordsToCheck []config.SockTable

	if args.SrcName != "" {
		if indices, ok := db.records.index[args.SrcName]; ok {
			for id := range indices {
				if rec, found := db.records.items[id]; found {
					recordsToCheck = append(recordsToCheck, rec)
				}
			}
		}
	} else {
		for _, val := range db.records.items {
			recordsToCheck = append(recordsToCheck, val)
		}
	}

	for _, record := range recordsToCheck {
		if args.SrcName != "" {
			if !strings.Contains(record.LocalAddr.Name, args.SrcName) &&
				!strings.Contains(record.RemoteAddr.Name, args.SrcName) {
				continue
			}
		}

		if args.Id != "" && record.Id != args.Id {
			continue
		}

		if args.Type != "" && record.Relation.Type != args.Type {
			continue
		}

		if args.Timestamp != "" {
			ts, err := strconv.ParseInt(args.Timestamp, 10, 64)
			if err == nil && record.Timestamp < ts {
				continue
			}
		}

		if args.RelationPort != "" {
			port, err := strconv.ParseUint(args.RelationPort, 10, 32)
			if err == nil && record.Relation.Port != uint32(port) {
				continue
			}
		}

		if args.RelationType != "" && record.Relation.Type != args.RelationType {
			continue
		}

		if args.RelationMode != "" && record.Relation.Mode != args.RelationMode {
			continue
		}

		if args.RelationResult != "" {
			result, err := strconv.ParseInt(args.RelationResult, 10, 32)
			if err == nil && record.Relation.Result != int32(result) {
				continue
			}
		}

		if args.RelationTrace != "" {
			trace, err := strconv.ParseInt(args.RelationTrace, 10, 32)
			if err == nil && record.Relation.Trace != int32(trace) {
				continue
			}
		}

		if args.RelationPing != "" {
			ping, err := strconv.ParseInt(args.RelationPing, 10, 32)
			if err == nil {
				if record.Relation.Ping != int32(ping) {
					continue
				}
			}
		}

		if args.RelationPacketLoss != "" {
			loss, err := strconv.ParseInt(args.RelationPacketLoss, 10, 32)
			if err == nil && record.Relation.PacketLoss != int32(loss) {
				continue
			}
		}

		if args.RelationMinRtt != "" {
			minRtt, err := strconv.ParseFloat(args.RelationMinRtt, 32)
			if err == nil && record.Relation.MinRtt != float32(minRtt) {
				continue
			}
		}

		if args.RelationMaxRtt != "" {
			maxRtt, err := strconv.ParseFloat(args.RelationMaxRtt, 32)
			if err == nil && record.Relation.MaxRtt != float32(maxRtt) {
				continue
			}
		}

		if args.RelationAvgRtt != "" {
			avgRtt, err := strconv.ParseFloat(args.RelationAvgRtt, 32)
			if err == nil && record.Relation.AvgRtt != float32(avgRtt) {
				continue
			}
		}

		if args.OptionsService != "" && record.Options.Service != args.OptionsService {
			continue
		}

		if args.OptionsStatus != "" && record.Options.Status != args.OptionsStatus {
			continue
		}

		if args.OptionsAccountId != "" {
			accountId, err := strconv.ParseUint(args.OptionsAccountId, 10, 32)
			if err == nil && record.Options.AccountID != uint32(accountId) {
				continue
			}
		}

		if args.OptionsSrcInfo != "" && !strings.Contains(record.Options.SrcInfo, args.OptionsSrcInfo) {
			continue
		}

		if args.OptionsDstInfo != "" && !strings.Contains(record.Options.DstInfo, args.OptionsDstInfo) {
			continue
		}

		if args.OptionsDescriptions != "" && !strings.Contains(record.Options.Descriptions, args.OptionsDescriptions) {
			continue
		}

		if args.LocalAddrName != "" && record.LocalAddr.Name != args.LocalAddrName {
			continue
		}

		if args.LocalAddrIp != "" && record.LocalAddr.IP != args.LocalAddrIp {
			continue
		}

		if args.LocalAddrPort != "" {
			port, err := strconv.ParseUint(args.LocalAddrPort, 10, 32)
			if err == nil && record.LocalAddr.Port != uint32(port) {
				continue
			}
		}

		if args.RemoteAddrName != "" && record.RemoteAddr.Name != args.RemoteAddrName {
			continue
		}

		if args.RemoteAddrIp != "" && record.RemoteAddr.IP != args.RemoteAddrIp {
			continue
		}

		if args.RemoteAddrPort != "" {
			port, err := strconv.ParseUint(args.RemoteAddrPort, 10, 32)
			if err == nil && record.RemoteAddr.Port != uint32(port) {
				continue
			}
		}

		items = append(items, record)
	}

	return items, nil
}

func (db *Client) WriteRecord(rec config.SockTable) error {
	sql := "replace into records (id,timestamp,localName,localIP,remoteName,remoteIP,relation,options) values (?,?,?,?,?,?,?,?)"

	relation, err := json.Marshal(rec.Relation)
	if err != nil {
		return err
	}

	options, err := json.Marshal(rec.Options)
	if err != nil {
		return err
	}

	localIP := net.ParseIP(rec.LocalAddr.IP)
	remoteIP := net.ParseIP(rec.RemoteAddr.IP)

	_, err = db.client.Exec(
		sql,
		rec.Id,
		time.Now().UTC().Unix(),
		rec.LocalAddr.Name,
		localIP,
		rec.RemoteAddr.Name,
		remoteIP,
		string(relation),
		string(options),
	)

	if err != nil {
		return err
	}

	return nil
}

func (db *Client) SaveRecord(rec config.SockTable) error {
	db.records.Lock()
	defer db.records.Unlock()

	if rec.Id == "" {
		rec.Id = config.GetIdRec(&rec)
	}

	item, found := db.records.items[rec.Id]
	if found && item.Timestamp > rec.Timestamp {
		return nil
	}

	if !found && len(db.records.items) >= db.config.Limit {
		return errors.New("cache limit exceeded")
	}

	if !found || (item.Relation != rec.Relation || item.Options != rec.Options) {
		if len(db.queue) < queue_limit {
			db.queue <- rec
		} else {
			log.Print("[error] DB write queue is full")
		}
	}

	if _, ok := db.records.index[rec.LocalAddr.Name]; !ok {
		db.records.index[rec.LocalAddr.Name] = make(map[string]bool)
	}

	db.records.index[rec.LocalAddr.Name][rec.Id] = true
	db.records.items[rec.Id] = rec

	return nil
}

func (db *Client) DelRecord(id string) error {
	db.records.Lock()
	defer db.records.Unlock()

	_, err := db.client.Exec("delete from records where id = ?", id)
	if err != nil {
		return err
	}

	rec, found := db.records.items[id]
	if !found {
		return nil
	}

	if _, ok := db.records.index[rec.LocalAddr.Name]; ok {
		if _, ok := db.records.index[rec.LocalAddr.Name][id]; ok {
			delete(db.records.index[rec.LocalAddr.Name], id)
		}
		if len(db.records.index[rec.LocalAddr.Name]) == 0 {
			delete(db.records.index, rec.LocalAddr.Name)
		}
	}

	delete(db.records.items, id)

	return nil
}

func (db *Client) LoadExceptions(args config.ExpArgs) ([]config.Exception, error) {
	db.exceptions.RLock()
	defer db.exceptions.RUnlock()

	items := []config.Exception{}

	if args.Id != "" {
		rec, found := db.exceptions.items[args.Id]
		if found {
			items = append(items, rec)
			return items, nil
		}
		return items, errors.New("object not found")
	}

	for _, val := range db.exceptions.items {
		if args.AccountID != "" {
			if fmt.Sprint(val.AccountID) != args.AccountID {
				continue
			}
		}

		items = append(items, val)
	}

	return items, nil
}

func (db *Client) SaveException(rec config.SockTable) error {
	db.exceptions.Lock()
	defer db.exceptions.Unlock()

	except, found := db.records.items[rec.Id]
	if found && except.Timestamp > rec.Timestamp {
		return nil
	}

	_, err := db.client.Exec(
		"replace into exceptions (id,accountId,hostMask,ignoreMask) values (?,?,?,?)",
		rec.Id,
		rec.Options.AccountID,
		rec.Options.HostMask,
		rec.Options.IgnoreMask,
	)

	if err != nil {
		return err
	}

	db.exceptions.items[rec.Id] = config.Exception{
		Id:         rec.Id,
		Timestamp:  rec.Timestamp,
		AccountID:  rec.Options.AccountID,
		HostMask:   rec.Options.HostMask,
		IgnoreMask: rec.Options.IgnoreMask,
	}

	return nil
}

func (db *Client) DelException(id string) error {
	db.exceptions.Lock()
	defer db.exceptions.Unlock()

	_, err := db.client.Exec("delete from exceptions where id = ?", id)
	if err != nil {
		return err
	}

	delete(db.exceptions.items, id)

	return nil
}
