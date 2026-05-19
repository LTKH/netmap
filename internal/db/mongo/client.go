package mongo

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ltkh/netmap/internal/config"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var (
	queue_limit = 1000000
)

type Client struct {
	records    Records
	exceptions Exceptions
	queue      chan config.SockTable
	client     *mongo.Client
	database   *mongo.Database
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

type RecordDocument struct {
	ID         string          `bson:"_id"`
	Timestamp  int64           `bson:"timestamp"`
	LocalAddr  config.SockAddr `bson:"localAddr"`
	RemoteAddr config.SockAddr `bson:"remoteAddr"`
	Relation   config.Relation `bson:"relation"`
	Options    config.Options  `bson:"options"`
}

type ExceptionDocument struct {
	ID         string `bson:"_id"`
	AccountID  uint32 `bson:"accountId"`
	HostMask   string `bson:"hostMask"`
	IgnoreMask string `bson:"ignoreMask"`
}

func New(conf *config.DB) (*Client, error) {
	ctx := context.Background()

	clientOptions := options.Client().ApplyURI(conf.ConnString)

	if conf.Username != "" && conf.Password != "" {
		clientOptions.SetAuth(options.Credential{
			Username: conf.Username,
			Password: conf.Password,
		})
	}

	client, err := mongo.Connect(ctx, clientOptions)
	if err != nil {
		return nil, err
	}

	err = client.Ping(ctx, nil)
	if err != nil {
		return nil, err
	}

	if conf.Limit == 0 {
		conf.Limit = 1000000
	}

	if conf.Name == "" {
		conf.Name = "netmap"
	}

	db := Client{
		records: Records{
			items: make(map[string]config.SockTable),
			index: make(map[string]map[string]bool),
		},
		exceptions: Exceptions{
			items: make(map[string]config.Exception),
		},
		queue:    make(chan config.SockTable, queue_limit),
		client:   client,
		database: client.Database(conf.Name),
		config:   conf,
	}

	go func() {
		for {
			rec := <-db.queue
			if err := db.writeRecord(rec); err != nil {
				log.Printf("[error] failed to write record to MongoDB: %v", err)
			}
		}
	}()

	return &db, nil
}

func (db *Client) Close() error {
	ctx := context.Background()
	return db.client.Disconnect(ctx)
}

func (db *Client) CreateTables() error {
	ctx := context.Background()

	recordsCollection := db.database.Collection("records")
	exceptionsCollection := db.database.Collection("exceptions")

	indexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "localAddr.name", Value: 1}},
			Options: options.Index().SetName("idx_local_name"),
		},
		{
			Keys:    bson.D{{Key: "remoteAddr.name", Value: 1}},
			Options: options.Index().SetName("idx_remote_name"),
		},
		{
			Keys:    bson.D{{Key: "localAddr.ip", Value: 1}},
			Options: options.Index().SetName("idx_local_ip"),
		},
		{
			Keys:    bson.D{{Key: "remoteAddr.ip", Value: 1}},
			Options: options.Index().SetName("idx_remote_ip"),
		},
		{
			Keys:    bson.D{{Key: "timestamp", Value: -1}},
			Options: options.Index().SetName("idx_timestamp"),
		},
		{
			Keys:    bson.D{{Key: "options.status", Value: 1}},
			Options: options.Index().SetName("idx_status"),
		},
		{
			Keys:    bson.D{{Key: "options.service", Value: 1}},
			Options: options.Index().SetName("idx_service"),
		},
		{
			Keys:    bson.D{{Key: "relation.mode", Value: 1}},
			Options: options.Index().SetName("idx_mode"),
		},
		{
			Keys:    bson.D{{Key: "relation.ping", Value: 1}},
			Options: options.Index().SetName("idx_ping"),
		},
		{
			Keys:    bson.D{{Key: "options.src_info", Value: 1}},
			Options: options.Index().SetName("idx_src_info"),
		},
		{
			Keys:    bson.D{{Key: "options.dst_info", Value: 1}},
			Options: options.Index().SetName("idx_dst_info"),
		},
		{
			Keys:    bson.D{{Key: "options.accountID", Value: 1}},
			Options: options.Index().SetName("idx_account_id"),
		},
	}

	_, err := recordsCollection.Indexes().CreateMany(ctx, indexes)
	if err != nil {
		return err
	}

	exceptionIndexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "accountId", Value: 1}},
			Options: options.Index().SetName("idx_exception_account"),
		},
		{
			Keys:    bson.D{{Key: "hostMask", Value: 1}},
			Options: options.Index().SetName("idx_host_mask"),
		},
	}

	_, err = exceptionsCollection.Indexes().CreateMany(ctx, exceptionIndexes)
	if err != nil {
		return err
	}

	return nil
}

func (db *Client) LoadTables() error {
	if err := db.loadTableRecords(); err != nil {
		return err
	}
	if err := db.loadTableExceptions(); err != nil {
		return err
	}
	return nil
}

func (db *Client) loadTableRecords() error {
	db.records.Lock()
	defer db.records.Unlock()

	ctx := context.Background()
	recordsCollection := db.database.Collection("records")

	opts := options.Find().SetLimit(int64(db.config.Limit))
	cursor, err := recordsCollection.Find(ctx, bson.M{}, opts)
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)

	for cursor.Next(ctx) {
		var doc RecordDocument
		if err := cursor.Decode(&doc); err != nil {
			log.Printf("[error] failed to decode record: %v", err)
			continue
		}

		rec := config.SockTable{
			Id:         doc.ID,
			Timestamp:  doc.Timestamp,
			LocalAddr:  doc.LocalAddr,
			RemoteAddr: doc.RemoteAddr,
			Relation:   doc.Relation,
			Options:    doc.Options,
		}

		if _, ok := db.records.index[rec.LocalAddr.Name]; !ok {
			db.records.index[rec.LocalAddr.Name] = make(map[string]bool)
		}

		db.records.index[rec.LocalAddr.Name][rec.Id] = true
		db.records.items[rec.Id] = rec
	}

	return nil
}

func (db *Client) loadTableExceptions() error {
	db.exceptions.Lock()
	defer db.exceptions.Unlock()

	ctx := context.Background()
	exceptionsCollection := db.database.Collection("exceptions")

	cursor, err := exceptionsCollection.Find(ctx, bson.M{})
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)

	for cursor.Next(ctx) {
		var doc ExceptionDocument
		if err := cursor.Decode(&doc); err != nil {
			log.Printf("[error] failed to decode exception: %v", err)
			continue
		}

		exp := config.Exception{
			Id:         doc.ID,
			AccountID:  doc.AccountID,
			HostMask:   doc.HostMask,
			IgnoreMask: doc.IgnoreMask,
		}

		db.exceptions.items[exp.Id] = exp
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

	if db.relationChanged(item.Relation, rec.Relation) || item.Options != rec.Options {
		item.Relation = rec.Relation
		item.Options = rec.Options
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

func (db *Client) relationChanged(old, new config.Relation) bool {
	if old.Mode != new.Mode ||
		old.Type != new.Type ||
		old.Port != new.Port ||
		old.Command != new.Command ||
		old.Result != new.Result ||
		old.Response != new.Response ||
		old.Trace != new.Trace ||
		old.Ping != new.Ping ||
		old.Packets != new.Packets ||
		old.PacketLoss != new.PacketLoss ||
		old.MinRtt != new.MinRtt ||
		old.MaxRtt != new.MaxRtt ||
		old.AvgRtt != new.AvgRtt {
		return true
	}
	return false
}

func (db *Client) LoadRecords(args config.RecArgs) ([]config.SockTable, error) {
	db.records.RLock()
	defer db.records.RUnlock()

	var items []config.SockTable

	if !db.hasFilters(args) && args.SrcName == "" {
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
		if db.matchFilters(record, args) {
			items = append(items, record)
		}
	}

	return items, nil
}

func (db *Client) hasFilters(args config.RecArgs) bool {
	return args.Id != "" || args.Type != "" || args.Timestamp != "" ||
		args.RelationPort != "" || args.RelationType != "" || args.RelationMode != "" ||
		args.RelationResult != "" || args.RelationTrace != "" ||
		args.RelationPing != "" || args.RelationPacketLoss != "" ||
		args.RelationMinRtt != "" || args.RelationMaxRtt != "" || args.RelationAvgRtt != "" ||
		args.OptionsService != "" || args.OptionsStatus != "" || args.OptionsAccountId != "" ||
		args.OptionsSrcInfo != "" || args.OptionsDstInfo != "" || args.OptionsDescriptions != "" ||
		args.LocalAddrName != "" || args.LocalAddrIp != "" || args.LocalAddrPort != "" ||
		args.RemoteAddrName != "" || args.RemoteAddrIp != "" || args.RemoteAddrPort != ""
}

func (db *Client) matchFilters(record config.SockTable, args config.RecArgs) bool {
	if args.SrcName != "" {
		if !strings.Contains(record.LocalAddr.Name, args.SrcName) &&
			!strings.Contains(record.RemoteAddr.Name, args.SrcName) {
			return false
		}
	}

	if args.Id != "" && record.Id != args.Id {
		return false
	}

	if args.Type != "" && record.Relation.Type != args.Type {
		return false
	}

	if args.Timestamp != "" {
		ts, err := strconv.ParseInt(args.Timestamp, 10, 64)
		if err == nil && record.Timestamp < ts {
			return false
		}
	}

	if args.RelationPort != "" {
		port, err := strconv.ParseUint(args.RelationPort, 10, 32)
		if err == nil && record.Relation.Port != uint32(port) {
			return false
		}
	}

	if args.RelationMode != "" && record.Relation.Mode != args.RelationMode {
		return false
	}

	if args.RelationResult != "" {
		result, err := strconv.ParseInt(args.RelationResult, 10, 32)
		if err == nil && record.Relation.Result != int32(result) {
			return false
		}
	}

	if args.RelationTrace != "" {
		trace, err := strconv.ParseInt(args.RelationTrace, 10, 32)
		if err == nil && record.Relation.Trace != int32(trace) {
			return false
		}
	}

	if args.RelationPing != "" {
		pingFilter, err := strconv.Atoi(args.RelationPing)
		if err == nil && record.Relation.Ping != int32(pingFilter) {
			return false
		}
	}

	if args.RelationPacketLoss != "" {
		loss, err := strconv.Atoi(args.RelationPacketLoss)
		if err == nil && record.Relation.PacketLoss != int32(loss) {
			return false
		}
	}

	if args.RelationMinRtt != "" {
		minRtt, err := strconv.ParseFloat(args.RelationMinRtt, 32)
		if err == nil && record.Relation.MinRtt != float32(minRtt) {
			return false
		}
	}

	if args.RelationMaxRtt != "" {
		maxRtt, err := strconv.ParseFloat(args.RelationMaxRtt, 32)
		if err == nil && record.Relation.MaxRtt != float32(maxRtt) {
			return false
		}
	}

	if args.RelationAvgRtt != "" {
		avgRtt, err := strconv.ParseFloat(args.RelationAvgRtt, 32)
		if err == nil && record.Relation.AvgRtt != float32(avgRtt) {
			return false
		}
	}

	if args.OptionsService != "" && record.Options.Service != args.OptionsService {
		return false
	}

	if args.OptionsStatus != "" && record.Options.Status != args.OptionsStatus {
		return false
	}

	if args.OptionsAccountId != "" {
		accountId, err := strconv.ParseUint(args.OptionsAccountId, 10, 32)
		if err == nil && record.Options.AccountID != uint32(accountId) {
			return false
		}
	}

	if args.OptionsSrcInfo != "" && !strings.Contains(record.Options.SrcInfo, args.OptionsSrcInfo) {
		return false
	}

	if args.OptionsDstInfo != "" && !strings.Contains(record.Options.DstInfo, args.OptionsDstInfo) {
		return false
	}

	if args.OptionsDescriptions != "" && !strings.Contains(record.Options.Descriptions, args.OptionsDescriptions) {
		return false
	}

	if args.LocalAddrName != "" && record.LocalAddr.Name != args.LocalAddrName {
		return false
	}

	if args.LocalAddrIp != "" && record.LocalAddr.IP != args.LocalAddrIp {
		return false
	}

	if args.LocalAddrPort != "" {
		port, err := strconv.ParseUint(args.LocalAddrPort, 10, 32)
		if err == nil && record.LocalAddr.Port != uint32(port) {
			return false
		}
	}

	if args.RemoteAddrName != "" && record.RemoteAddr.Name != args.RemoteAddrName {
		return false
	}

	if args.RemoteAddrIp != "" && record.RemoteAddr.IP != args.RemoteAddrIp {
		return false
	}

	if args.RemoteAddrPort != "" {
		port, err := strconv.ParseUint(args.RemoteAddrPort, 10, 32)
		if err == nil && record.RemoteAddr.Port != uint32(port) {
			return false
		}
	}

	return true
}

func (db *Client) SaveRecord(rec config.SockTable) error {
	db.records.Lock()
	defer db.records.Unlock()

	if rec.Id == "" {
		rec.Id = config.GetIdRec(&rec)
	}

	log.Printf("[debug] SaveRecord: ID=%s, Ping=%d, Packets=%d, PacketLoss=%d, MinRtt=%.2f, MaxRtt=%.2f, AvgRtt=%.2f",
		rec.Id, rec.Relation.Ping, rec.Relation.Packets, rec.Relation.PacketLoss,
		rec.Relation.MinRtt, rec.Relation.MaxRtt, rec.Relation.AvgRtt)

	item, found := db.records.items[rec.Id]
	if found && item.Timestamp > rec.Timestamp {
		return nil
	}

	if !found && len(db.records.items) >= db.config.Limit {
		return errors.New("cache limit exceeded")
	}

	needSave := false
	if !found {
		needSave = true
	} else if db.relationChanged(item.Relation, rec.Relation) || item.Options != rec.Options {
		needSave = true
	}

	if needSave {
		if len(db.queue) < queue_limit {
			db.queue <- rec
			log.Printf("[debug] Queued record for save: ID=%s, Ping=%d", rec.Id, rec.Relation.Ping)
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

func (db *Client) writeRecord(rec config.SockTable) error {
	ctx := context.Background()
	recordsCollection := db.database.Collection("records")

	log.Printf("[debug] writeRecord to MongoDB: ID=%s, Ping=%d, Packets=%d, PacketLoss=%d, MinRtt=%.2f, MaxRtt=%.2f, AvgRtt=%.2f",
		rec.Id, rec.Relation.Ping, rec.Relation.Packets, rec.Relation.PacketLoss,
		rec.Relation.MinRtt, rec.Relation.MaxRtt, rec.Relation.AvgRtt)

	doc := RecordDocument{
		ID:         rec.Id,
		Timestamp:  time.Now().UTC().Unix(),
		LocalAddr:  rec.LocalAddr,
		RemoteAddr: rec.RemoteAddr,
		Relation:   rec.Relation,
		Options:    rec.Options,
	}

	opts := options.Replace().SetUpsert(true)
	_, err := recordsCollection.ReplaceOne(ctx, bson.M{"_id": rec.Id}, doc, opts)

	if err != nil {
		log.Printf("[error] Failed to write record to MongoDB: %v", err)
	} else {
		log.Printf("[debug] Successfully wrote record to MongoDB: ID=%s", rec.Id)
	}

	return err
}

func (db *Client) DelRecord(id string) error {
	db.records.Lock()
	defer db.records.Unlock()

	ctx := context.Background()
	recordsCollection := db.database.Collection("records")

	_, err := recordsCollection.DeleteOne(ctx, bson.M{"_id": id})
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

	ctx := context.Background()
	exceptionsCollection := db.database.Collection("exceptions")

	doc := ExceptionDocument{
		ID:         rec.Id,
		AccountID:  rec.Options.AccountID,
		HostMask:   rec.Options.HostMask,
		IgnoreMask: rec.Options.IgnoreMask,
	}

	opts := options.Replace().SetUpsert(true)
	_, err := exceptionsCollection.ReplaceOne(ctx, bson.M{"_id": rec.Id}, doc, opts)

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

	ctx := context.Background()
	exceptionsCollection := db.database.Collection("exceptions")

	_, err := exceptionsCollection.DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return err
	}

	delete(db.exceptions.items, id)

	return nil
}
