package v1

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ltkh/netmap/internal/client"
	"github.com/ltkh/netmap/internal/config"
	"github.com/ltkh/netmap/internal/db"
	"github.com/prometheus/client_golang/prometheus"

	pb "github.com/ltkh/netmap/internal/grpc"
)

var (
	httpClient = client.NewHttpClient(nil)

	resultCode = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "netmap",
			Name:      "result_code",
			Help:      "",
		},
		[]string{"src_name", "dst_name", "mode", "port"},
	)

	responseTime = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "netmap",
			Name:      "response_time",
			Help:      "",
		},
		[]string{"src_name", "dst_name", "mode", "port"},
	)
)

type Api struct {
	Conf    *config.Config        `json:"conf"`
	DB      *db.DbClient          `json:"db"`
	Collect chan config.SockTable `json:"-"`
	Server  *Server               `json:"-"`
}

type Resp struct {
	Status   string        `json:"status"`
	Error    string        `json:"error,omitempty"`
	Warnings []string      `json:"warnings,omitempty"`
	Data     []interface{} `json:"data"`
}

type Records struct {
	sync.RWMutex
	items map[string]config.SockTable
}

type Exceptions struct {
	sync.RWMutex
	items map[string]config.Exception
}

func readUserIP(r *http.Request) string {
	IPAddress := r.Header.Get("X-Real-Ip")
	if IPAddress == "" {
		IPAddress = r.Header.Get("X-Forwarded-For")
	}
	if IPAddress == "" {
		IPAddress = r.RemoteAddr
	}
	return IPAddress
}

func encodeResp(resp *Resp) []byte {
	if len(resp.Data) == 0 {
		resp.Data = make([]interface{}, 0)
	}

	jsn, err := json.Marshal(resp)
	if err != nil {
		return encodeResp(&Resp{Status: "error", Error: err.Error(), Data: make([]interface{}, 0)})
	}
	return jsn
}

func compressData(data []byte, encoding string) (bytes.Buffer, bool, error) {
	var buf bytes.Buffer
	matched, _ := regexp.MatchString(`gzip`, encoding)
	if matched {
		writer := gzip.NewWriter(&buf)
		if _, err := writer.Write(data); err != nil {
			return buf, false, fmt.Errorf("unable to compress data")
		}
		if err := writer.Close(); err != nil {
			return buf, false, fmt.Errorf("unable to compress data")
		}
		return buf, true, nil
	}

	return *bytes.NewBuffer(data), false, nil
}

func MonRegister() {
	prometheus.MustRegister(resultCode)
	prometheus.MustRegister(responseTime)
}

func NewAPI(debug bool, conf *config.Config, peers []string, db db.DbClient, srv *Server) (*Api, error) {
	if err := db.CreateTables(); err != nil {
		return &Api{}, err
	}

	if err := db.LoadTables(); err != nil {
		return &Api{}, err
	}

	api := &Api{
		Conf:    conf,
		DB:      &db,
		Collect: make(chan config.SockTable, 1000000),
		Server:  srv,
	}

	rpc := &Rpc{
		Debug: debug,
		Peers: peers,
		DB:    &db,
	}

	go api.SendToCollect()
	go rpc.RunGrpcClient()

	return api, nil
}

func (api *Api) SendToCollect() {
	for {
		var netstat config.NetstatData

		for i := 0; i < len(api.Collect); i++ {
			rec := <-api.Collect
			netstat.Data = append(netstat.Data, rec)
		}

		body, err := json.Marshal(netstat)
		if err != nil {
			log.Printf("[error] %v", err)
			continue
		}

		if len(netstat.Data) > 0 {
			config := client.HttpConfig{
				URLs:     api.Conf.Collector.URLs,
				Username: api.Conf.Collector.Username,
				Password: api.Conf.Collector.Password,
			}

			if err := httpClient.WriteRecords(config, api.Conf.Collector.Path, body); err != nil {
				log.Printf("[error] %v (%v)", err, len(netstat.Data))
			}
		}

		time.Sleep(15 * time.Second)
	}
}

func (api *Api) ApiHealthy(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(200)
	w.Write([]byte("OK"))
}

func (api *Api) ApiStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	user, pass, _ := r.BasicAuth()
	if len(api.Conf.Global.Users) > 0 {
		if ps, ok := api.Conf.Global.Users[user]; !ok || ps != pass {
			w.WriteHeader(403)
			w.Write(encodeResp(&Resp{Status: "error", Error: "access is denied"}))
			return
		}
	}

	if r.Method == "POST" {
		var reader io.ReadCloser
		var err error

		switch r.Header.Get("Content-Encoding") {
		case "gzip":
			reader, err = gzip.NewReader(r.Body)
			if err != nil {
				log.Printf("[error] %v - %s", err, r.URL.Path)
				w.WriteHeader(400)
				w.Write(encodeResp(&Resp{Status: "error", Error: err.Error()}))
				return
			}
			defer reader.Close()
		default:
			reader = r.Body
		}
		defer r.Body.Close()

		body, err := ioutil.ReadAll(reader)
		if err != nil {
			log.Printf("[error] %v - %s", err, r.URL.Path)
			w.WriteHeader(400)
			w.Write(encodeResp(&Resp{Status: "error", Error: err.Error()}))
			return
		}

		var netstat config.NetstatData

		if err := json.Unmarshal(body, &netstat); err != nil {
			log.Printf("[error] %v - %s", err, r.URL.Path)
			w.WriteHeader(400)
			w.Write(encodeResp(&Resp{Status: "error", Error: err.Error()}))
			return
		}

		for _, rc := range netstat.Data {
			if rc.Id == "" {
				rc.Id = config.GetIdRec(&rc)
			}

			if rc.Timestamp == 0 {
				rc.Timestamp = time.Now().UTC().Unix()
			}

			if err := db.DbClient.SaveStatus(*api.DB, rc); err != nil {
				w.WriteHeader(500)
				w.Write(encodeResp(&Resp{Status: "error", Error: err.Error()}))
				return
			}

			event := convertRec(ServerId, "setStatus", rc)
			api.Server.Broadcast(event)
		}

		w.WriteHeader(204)
		return
	}

	w.WriteHeader(405)
	w.Write(encodeResp(&Resp{Status: "error", Error: "method not allowed"}))
}

func (api *Api) ApiNetstat(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	user, pass, _ := r.BasicAuth()
	if len(api.Conf.Global.Users) > 0 {
		if ps, ok := api.Conf.Global.Users[user]; !ok || ps != pass {
			w.WriteHeader(403)
			w.Write(encodeResp(&Resp{Status: "error", Error: "access is denied"}))
			return
		}
	}

	if r.Method == "POST" {
		var reader io.ReadCloser
		var err error

		switch r.Header.Get("Content-Encoding") {
		case "gzip":
			reader, err = gzip.NewReader(r.Body)
			if err != nil {
				log.Printf("[error] %v - %s", err, r.URL.Path)
				w.WriteHeader(400)
				w.Write(encodeResp(&Resp{Status: "error", Error: err.Error()}))
				return
			}
			defer reader.Close()
		default:
			reader = r.Body
		}
		defer r.Body.Close()

		body, err := ioutil.ReadAll(reader)
		if err != nil {
			log.Printf("[error] %v - %s", err, r.URL.Path)
			w.WriteHeader(400)
			w.Write(encodeResp(&Resp{Status: "error", Error: err.Error()}))
			return
		}

		var netstat config.NetstatData

		if err := json.Unmarshal(body, &netstat); err != nil {
			log.Printf("[error] %v - %s", err, r.URL.Path)
			w.WriteHeader(400)
			w.Write(encodeResp(&Resp{Status: "error", Error: err.Error()}))
			return
		}

		if len(api.Conf.Collector.URLs) > 0 {
			for _, rec := range netstat.Data {
				if api.Conf.Collector.Prepare {
					localAddr := rec.LocalAddr
					remoteAddr := rec.RemoteAddr
					if rec.Relation.Type == "incoming" {
						rec.LocalAddr = remoteAddr
						rec.RemoteAddr = localAddr
						rec.Relation.Type = ""
					}
					rec.Relation.Port = rec.RemoteAddr.Port
					rec.Timestamp = -1
				}
				select {
				case api.Collect <- rec:
				default:

				}
			}
		}

		w.WriteHeader(204)
		return
	}

	w.WriteHeader(405)
	w.Write(encodeResp(&Resp{Status: "error", Error: "method not allowed"}))
}

func (api *Api) ApiTracert(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	user, pass, _ := r.BasicAuth()
	if len(api.Conf.Global.Users) > 0 {
		if ps, ok := api.Conf.Global.Users[user]; !ok || ps != pass {
			w.WriteHeader(403)
			w.Write(encodeResp(&Resp{Status: "error", Error: "access is denied"}))
			return
		}
	}

	if r.Method == "POST" {
		var reader io.ReadCloser
		var err error

		switch r.Header.Get("Content-Encoding") {
		case "gzip":
			reader, err = gzip.NewReader(r.Body)
			if err != nil {
				log.Printf("[error] %v - %s", err, r.URL.Path)
				w.WriteHeader(400)
				w.Write(encodeResp(&Resp{Status: "error", Error: err.Error()}))
				return
			}
			defer reader.Close()
		default:
			reader = r.Body
		}
		defer r.Body.Close()

		body, err := ioutil.ReadAll(reader)
		if err != nil {
			log.Printf("[error] %v - %s", err, r.URL.Path)
			w.WriteHeader(400)
			w.Write(encodeResp(&Resp{Status: "error", Error: err.Error()}))
			return
		}

		var netstat config.NetstatData

		if err := json.Unmarshal(body, &netstat); err != nil {
			log.Printf("[error] %v - %s", err, r.URL.Path)
			w.WriteHeader(400)
			w.Write(encodeResp(&Resp{Status: "error", Error: err.Error()}))
			return
		}

		for _, rc := range netstat.Data {
			if rc.Id == "" {
				rc.Id = config.GetIdRec(&rc)
			}

			if rc.Timestamp == 0 {
				rc.Timestamp = time.Now().UTC().Unix()
			}

			if err := db.DbClient.SaveTracert(*api.DB, rc); err != nil {
				w.WriteHeader(500)
				w.Write(encodeResp(&Resp{Status: "error", Error: err.Error()}))
				return
			}

			event := convertRec(ServerId, "setTracert", rc)
			api.Server.Broadcast(event)
		}

		w.WriteHeader(204)
		return
	}

	w.WriteHeader(405)
	w.Write(encodeResp(&Resp{Status: "error", Error: "method not allowed"}))
}

func (api *Api) ApiRecords(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	user, pass, _ := r.BasicAuth()
	if len(api.Conf.Global.Users) > 0 {
		if ps, ok := api.Conf.Global.Users[user]; !ok || ps != pass {
			w.WriteHeader(403)
			w.Write(encodeResp(&Resp{Status: "error", Error: "access is denied"}))
			return
		}
	}

	if r.Method == "GET" {

		var args config.RecArgs

		for k, v := range r.URL.Query() {
			switch k {
			case "id":
				args.Id = v[0]
			case "type":
				args.Type = v[0]
			case "src_name":
				args.SrcName = v[0]
			case "timestamp":
				args.Timestamp = v[0]
			case "relation_port":
				args.RelationPort = v[0]
			case "relation_type":
				args.RelationType = v[0]
			case "relation_mode":
				args.RelationMode = v[0]
			case "relation_result":
				args.RelationResult = v[0]
			case "relation_trace":
				args.RelationTrace = v[0]
			case "options_service":
				args.OptionsService = v[0]
			case "options_status":
				args.OptionsStatus = v[0]
			case "options_accountid":
				args.OptionsAccountId = v[0]
			case "local_addr_name":
				args.LocalAddrName = v[0]
			case "local_addr_ip":
				args.LocalAddrIp = v[0]
			case "remote_addr_name":
				args.RemoteAddrName = v[0]
			case "remote_addr_ip":
				args.RemoteAddrIp = v[0]
			}
		}

		items, err := db.DbClient.LoadRecords(*api.DB, args)
		if err != nil {
			log.Printf("[error] %v - %s", err, r.URL.Path)
			w.WriteHeader(500)
			w.Write(encodeResp(&Resp{Status: "error", Error: err.Error()}))
			return
		}

		var records []interface{}
		for _, item := range items {
			if args.Timestamp != "" {
				tsStr := args.Timestamp

				var operator string
				if len(tsStr) > 1 {
					switch tsStr[0] {
					case '>':
						if tsStr[1] == '=' {
							operator = ">="
							tsStr = tsStr[2:]
						} else {
							operator = ">"
							tsStr = tsStr[1:]
						}
					case '<':
						if tsStr[1] == '=' {
							operator = "<="
							tsStr = tsStr[2:]
						} else {
							operator = "<"
							tsStr = tsStr[1:]
						}
					case '=':
						operator = "="
						tsStr = tsStr[1:]
					}
				}

				ts, err := strconv.ParseInt(tsStr, 10, 64)
				if err == nil {
					switch operator {
					case ">":
						if item.Timestamp <= ts {
							continue
						}
					case ">=":
						if item.Timestamp < ts {
							continue
						}
					case "<":
						if item.Timestamp >= ts {
							continue
						}
					case "<=":
						if item.Timestamp > ts {
							continue
						}
					case "=":
						if item.Timestamp != ts {
							continue
						}
					default:
						if item.Timestamp < ts {
							continue
						}
					}
				}
			}
			records = append(records, item)
		}

		data := encodeResp(&Resp{Status: "success", Data: records})

		w.WriteHeader(200)
		w.Write(data)
		return
	}

	if r.Method == "POST" {
		var reader io.ReadCloser
		var err error

		switch r.Header.Get("Content-Encoding") {
		case "gzip":
			reader, err = gzip.NewReader(r.Body)
			if err != nil {
				log.Printf("[error] %v - %s", err, r.URL.Path)
				w.WriteHeader(400)
				w.Write(encodeResp(&Resp{Status: "error", Error: err.Error()}))
				return
			}
			defer reader.Close()
		default:
			reader = r.Body
		}
		defer r.Body.Close()

		body, err := ioutil.ReadAll(reader)
		if err != nil {
			log.Printf("[error] %v - %s", err, r.URL.Path)
			w.WriteHeader(400)
			w.Write(encodeResp(&Resp{Status: "error", Error: err.Error()}))
			return
		}

		var recData config.RecordsData

		if err := json.Unmarshal(body, &recData); err != nil {
			log.Printf("[error] %v - %s", err, r.URL.Path)
			w.WriteHeader(400)
			w.Write(encodeResp(&Resp{Status: "error", Error: err.Error()}))
			return
		}

		for _, rc := range recData.Data {
			if rc.Id == "" {
				rc.Id = config.GetIdRec(&rc)
			}
			if rc.Timestamp == 0 {
				rc.Timestamp = time.Now().UTC().Unix()
			}
			jsonRC, err := json.Marshal(rc)
			if err != nil {
				log.Printf("[error] marshaling to JSON:", err)
				continue
			}
			if rc.LocalAddr.Name == "" {
				log.Printf("[warn] %s: parameter missing localAddr.name", string(jsonRC))
				continue
			}
			if rc.LocalAddr.IP == "" {
				log.Printf("[warn] %s: parameter missing LocalAddr.ip", string(jsonRC))
				continue
			}
			if rc.RemoteAddr.Name == "" {
				log.Printf("[warn] %s: parameter missing remoteAddr.name", string(jsonRC))
				continue
			}
			if rc.RemoteAddr.IP == "" {
				log.Printf("[warn] %s: parameter missing remoteAddr.ip", string(jsonRC))
				continue
			}
			if rc.Relation.Mode != "cmd" && rc.Relation.Port == 0 {
				log.Printf("[warn] %s: parameter missing relation.port", string(jsonRC))
				continue
			}
			if rc.Relation.Mode == "" {
				log.Printf("[warn] %s: parameter missing relation.mode", string(jsonRC))
				continue
			}

			if err := db.DbClient.SaveRecord(*api.DB, rc); err != nil {
				log.Printf("[error] %v - %s", err.Error(), r.URL.Path)
				w.WriteHeader(500)
				return
			}

			event := convertRec(ServerId, "setRecord", rc)
			api.Server.Broadcast(event)
		}

		w.WriteHeader(204)
		return
	}

	if r.Method == "DELETE" {
		var reader io.ReadCloser
		var err error

		switch r.Header.Get("Content-Encoding") {
		case "gzip":
			reader, err = gzip.NewReader(r.Body)
			if err != nil {
				log.Printf("[error] %v - %s", err, r.URL.Path)
				w.WriteHeader(400)
				w.Write(encodeResp(&Resp{Status: "error", Error: err.Error()}))
				return
			}
			defer reader.Close()
		default:
			reader = r.Body
		}
		defer r.Body.Close()

		body, err := ioutil.ReadAll(reader)
		if err != nil {
			log.Printf("[error] %v - %s", err, r.URL.Path)
			w.WriteHeader(400)
			w.Write(encodeResp(&Resp{Status: "error", Error: err.Error()}))
			return
		}

		var keys []string

		if err := json.Unmarshal(body, &keys); err != nil {
			log.Printf("[error] %v - %s", err, r.URL.Path)
			w.WriteHeader(400)
			w.Write(encodeResp(&Resp{Status: "error", Error: err.Error()}))
			return
		}

		for _, id := range keys {
			if err := db.DbClient.DelRecord(*api.DB, id); err != nil {
				w.WriteHeader(500)
				w.Write(encodeResp(&Resp{Status: "error", Error: err.Error()}))
				return
			}

			event := &pb.Event{
				ServerId: ServerId,
				Event:    "delRecord",
				Id:       id,
			}
			api.Server.Broadcast(event)
		}

		w.WriteHeader(200)
		w.Write(encodeResp(&Resp{Status: "success"}))
		return
	}

	w.WriteHeader(405)
	w.Write(encodeResp(&Resp{Status: "error", Error: "method not allowed"}))
}

func (api *Api) ApiTotalCount(w http.ResponseWriter, r *http.Request) {

	items, err := db.DbClient.LoadRecords(*api.DB, config.RecArgs{})
	if err != nil {
		log.Printf("[error] failed to load records: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"total_count": len(items),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (api *Api) ApiCountRecords(w http.ResponseWriter, r *http.Request) {

	var args config.RecArgs

	for k, v := range r.URL.Query() {
		switch k {
		case "id":
			args.Id = v[0]
		case "type":
			args.Type = v[0]
		case "src_name":
			args.SrcName = v[0]
		case "timestamp":
			args.Timestamp = v[0]
		case "relation_port":
			args.RelationPort = v[0]
		case "relation_type":
			args.RelationType = v[0]
		case "relation_mode":
			args.RelationMode = v[0]
		case "relation_result":
			args.RelationResult = v[0]
		case "relation_trace":
			args.RelationTrace = v[0]
		case "options_service":
			args.OptionsService = v[0]
		case "options_status":
			args.OptionsStatus = v[0]
		case "options_accountid":
			args.OptionsAccountId = v[0]
		case "local_addr_name":
			args.LocalAddrName = v[0]
		case "local_addr_ip":
			args.LocalAddrIp = v[0]
		case "local_addr_port":
			args.LocalAddrPort = v[0]
		case "remote_addr_name":
			args.RemoteAddrName = v[0]
		case "remote_addr_ip":
			args.RemoteAddrIp = v[0]
		case "remote_addr_port":
			args.RemoteAddrPort = v[0]
		}
	}

	items, err := db.DbClient.LoadRecords(*api.DB, args)
	if err != nil {
		log.Printf("[error] failed to load records: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"count":           len(items),
		"filters_applied": args,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (api *Api) ApiExceptions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	user, pass, _ := r.BasicAuth()
	if len(api.Conf.Global.Users) > 0 {
		if ps, ok := api.Conf.Global.Users[user]; !ok || ps != pass {
			w.WriteHeader(403)
			w.Write(encodeResp(&Resp{Status: "error", Error: "access is denied"}))
			return
		}
	}

	if r.Method == "GET" {
		var args config.ExpArgs

		for k, v := range r.URL.Query() {
			switch k {
			case "id":
				args.Id = v[0]
			case "src_name":
				args.SrcName = v[0]
			case "account_id":
				args.AccountID = v[0]
			}
		}

		items, err := db.DbClient.LoadExceptions(*api.DB, args)
		if err != nil {
			w.WriteHeader(500)
			w.Write(encodeResp(&Resp{Status: "error", Error: err.Error()}))
			return
		}

		var exceptions []interface{}
		for _, item := range items {
			exceptions = append(exceptions, item)
		}

		data := encodeResp(&Resp{Status: "success", Data: exceptions})
		buf, ok, err := compressData(data, r.Header.Get("Accept-Encoding"))
		if err != nil {
			log.Printf("[error] %v - %s", err, r.URL.Path)
			w.WriteHeader(500)
			w.Write(encodeResp(&Resp{Status: "error", Error: err.Error()}))
			return
		}
		if ok {
			w.Header().Set("Content-Encoding", "gzip")
		}

		w.WriteHeader(200)
		w.Write(buf.Bytes())
		return
	}

	if r.Method == "POST" {
		var reader io.ReadCloser
		var err error

		switch r.Header.Get("Content-Encoding") {
		case "gzip":
			reader, err = gzip.NewReader(r.Body)
			if err != nil {
				log.Printf("[error] %v - %s", err, r.URL.Path)
				w.WriteHeader(400)
				w.Write(encodeResp(&Resp{Status: "error", Error: err.Error()}))
				return
			}
			defer reader.Close()
		default:
			reader = r.Body
		}
		defer r.Body.Close()

		body, err := ioutil.ReadAll(reader)
		if err != nil {
			log.Printf("[error] %v - %s", err, r.URL.Path)
			w.WriteHeader(400)
			w.Write(encodeResp(&Resp{Status: "error", Error: err.Error()}))
			return
		}

		var expdata config.ExceptionData

		if err := json.Unmarshal(body, &expdata); err != nil {
			log.Printf("[error] %v - %s", err, r.URL.Path)
			w.WriteHeader(400)
			w.Write(encodeResp(&Resp{Status: "error", Error: err.Error()}))
			return
		}

		for _, ex := range expdata.Data {
			if ex.Id == "" {
				ex.Id = config.GetIdExp(&ex)
			}

			if ex.Timestamp == 0 {
				ex.Timestamp = time.Now().UTC().Unix()
			}

			rc := config.SockTable{
				Id:        ex.Id,
				Timestamp: ex.Timestamp,
				Options: config.Options{
					AccountID:  ex.AccountID,
					HostMask:   ex.HostMask,
					IgnoreMask: ex.IgnoreMask,
				},
			}

			if err := db.DbClient.SaveException(*api.DB, rc); err != nil {
				w.WriteHeader(500)
				w.Write(encodeResp(&Resp{Status: "error", Error: err.Error()}))
				return
			}

			event := convertRec(ServerId, "setException", rc)
			api.Server.Broadcast(event)
		}

		w.WriteHeader(204)
		return
	}

	if r.Method == "DELETE" {
		var reader io.ReadCloser
		var err error

		switch r.Header.Get("Content-Encoding") {
		case "gzip":
			reader, err = gzip.NewReader(r.Body)
			if err != nil {
				log.Printf("[error] %v - %s", err, r.URL.Path)
				w.WriteHeader(400)
				w.Write(encodeResp(&Resp{Status: "error", Error: err.Error()}))
				return
			}
			defer reader.Close()
		default:
			reader = r.Body
		}
		defer r.Body.Close()

		body, err := ioutil.ReadAll(reader)
		if err != nil {
			log.Printf("[error] %v - %s", err, r.URL.Path)
			w.WriteHeader(400)
			w.Write(encodeResp(&Resp{Status: "error", Error: err.Error()}))
			return
		}

		var keys []string

		if err := json.Unmarshal(body, &keys); err != nil {
			log.Printf("[error] %v - %s", err, r.URL.Path)
			w.WriteHeader(400)
			w.Write(encodeResp(&Resp{Status: "error", Error: err.Error()}))
			return
		}

		for _, id := range keys {
			if err := db.DbClient.DelException(*api.DB, id); err != nil {
				w.WriteHeader(500)
				w.Write(encodeResp(&Resp{Status: "error", Error: err.Error()}))
				return
			}

			event := &pb.Event{
				ServerId: ServerId,
				Event:    "delException",
				Id:       id,
			}
			api.Server.Broadcast(event)
		}

		w.WriteHeader(200)
		w.Write(encodeResp(&Resp{Status: "success"}))
		return
	}

	w.WriteHeader(405)
	w.Write(encodeResp(&Resp{Status: "error", Error: "method not allowed"}))
}

func (api *Api) ApiWebhook(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	user, pass, _ := r.BasicAuth()
	if len(api.Conf.Global.Users) > 0 {
		if ps, ok := api.Conf.Global.Users[user]; !ok || ps != pass {
			w.WriteHeader(403)
			w.Write(encodeResp(&Resp{Status: "error", Error: "access is denied"}))
			return
		}
	}

	if r.Method == "POST" {
		var reader io.ReadCloser
		var err error

		switch r.Header.Get("Content-Encoding") {
		case "gzip":
			reader, err = gzip.NewReader(r.Body)
			if err != nil {
				log.Printf("[error] %v - %s", err, r.URL.Path)
				w.WriteHeader(400)
				w.Write(encodeResp(&Resp{Status: "error", Error: err.Error()}))
				return
			}
			defer reader.Close()
		default:
			reader = r.Body
		}
		defer r.Body.Close()

		body, err := ioutil.ReadAll(reader)
		if err != nil {
			log.Printf("[error] %v - %s", err, r.URL.Path)
			w.WriteHeader(400)
			w.Write(encodeResp(&Resp{Status: "error", Error: err.Error()}))
			return
		}

		if len(api.Conf.Notifier.URLs) > 0 {
			for _, url := range api.Conf.Notifier.URLs {
				config := client.HttpConfig{
					URLs:     []string{url},
					Username: api.Conf.Notifier.Username,
					Password: api.Conf.Notifier.Password,
				}
				go httpClient.WriteRecords(config, api.Conf.Notifier.Path, body)
			}
		}

		w.WriteHeader(204)
		return
	}

	w.WriteHeader(405)
	w.Write(encodeResp(&Resp{Status: "error", Error: "method not allowed"}))
}

func (api *Api) ApiIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	html := `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Netmap Dashboard</title>
    <style>
        * {
            margin: 0;
            padding: 0;
            box-sizing: border-box;
            font-family: 'Segoe UI', Tahoma, Geneva, Verdana, sans-serif;
        }
        
        body {
            background-color: #f5f5f5;
            color: #333;
            line-height: 1.6;
        }
        
        .container {
            max-width: 1400px;
            margin: 0 auto;
            padding: 20px;
        }
        
        header {
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            color: white;
            padding: 2rem 0;
            text-align: center;
            border-radius: 0 0 20px 20px;
            box-shadow: 0 4px 6px rgba(0, 0, 0, 0.1);
            margin-bottom: 2rem;
        }
        
        header h1 {
            font-size: 2.5rem;
            margin-bottom: 0.5rem;
        }
        
        header p {
            font-size: 1.1rem;
            opacity: 0.9;
        }
        
        .dashboard-stats {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(250px, 1fr));
            gap: 1.5rem;
            margin-bottom: 2rem;
        }
        
        .stat-card {
            background: white;
            padding: 1.5rem;
            border-radius: 10px;
            box-shadow: 0 2px 4px rgba(0, 0, 0, 0.1);
            transition: transform 0.3s ease;
        }
        
        .stat-card:hover {
            transform: translateY(-5px);
            box-shadow: 0 4px 8px rgba(0, 0, 0, 0.15);
        }
        
        .stat-card h3 {
            color: #667eea;
            margin-bottom: 0.5rem;
            font-size: 1.2rem;
        }
        
        .stat-value {
            font-size: 2rem;
            font-weight: bold;
            color: #333;
        }
        
        .nav-links {
            display: flex;
            flex-wrap: wrap;
            gap: 1rem;
            margin-bottom: 2rem;
        }
        
        .nav-link {
            display: inline-block;
            padding: 0.8rem 1.5rem;
            background: white;
            color: #667eea;
            text-decoration: none;
            border-radius: 8px;
            font-weight: 600;
            box-shadow: 0 2px 4px rgba(0, 0, 0, 0.1);
            transition: all 0.3s ease;
        }
        
        .nav-link:hover {
            background: #667eea;
            color: white;
            transform: translateY(-2px);
        }
        
        .api-info {
            background: white;
            padding: 1.5rem;
            border-radius: 10px;
            margin-top: 2rem;
        }
        
        .api-info h3 {
            color: #667eea;
            margin-bottom: 1rem;
        }
        
        .endpoint {
            background: #f8f9fa;
            padding: 1rem;
            border-radius: 5px;
            margin-bottom: 0.5rem;
            font-family: 'Courier New', monospace;
        }
        
        footer {
            text-align: center;
            margin-top: 3rem;
            padding: 1rem;
            color: #666;
            font-size: 0.9rem;
        }
        
        .quick-stats {
            display: flex;
            justify-content: space-around;
            flex-wrap: wrap;
            margin: 1rem 0;
        }
        
        .quick-stat {
            text-align: center;
            padding: 1rem;
        }
        
        .quick-stat .value {
            font-size: 1.5rem;
            font-weight: bold;
            color: #667eea;
        }
        
        .quick-stat .label {
            font-size: 0.9rem;
            color: #666;
        }
    </style>
</head>
<body>
    <header>
        <div class="container">
            <h1>Netmap Dashboard</h1>
            <p>Network connection monitoring and visualization</p>
        </div>
    </header>
    
    <div class="container">
        <div class="dashboard-stats" id="stats">
            <div class="stat-card">
                <h3>Total Connections</h3>
                <div class="stat-value" id="total-connections">Loading...</div>
                <p>Total number of recorded connections</p>
            </div>
            
            <div class="stat-card">
                <h3>Active Services</h3>
                <div class="stat-value" id="active-services">-</div>
                <p>Unique services being monitored</p>
            </div>
            
            <div class="stat-card">
                <h3>Today's Records</h3>
                <div class="stat-value" id="today-records">-</div>
                <p>Connections recorded today</p>
            </div>
            
            <div class="stat-card">
                <h3>API Status</h3>
                <div class="stat-value" style="color: #4CAF50;" id="api-status">✓ Online</div>
                <p>Backend services status</p>
            </div>
        </div>
        
        <div class="nav-links">
            <a href="/records" class="nav-link">View All Records</a>
            <a href="/records?options_status=netmap" class="nav-link">Active Connections</a>
            <a href="/records?relation_mode=tcp" class="nav-link">TCP Connections</a>
            <a href="/records?relation_mode=udp" class="nav-link">UDP Connections</a>
            <a href="/filters" class="nav-link">Advanced Search</a>
            <a href="/api/v1/netmap/records" class="nav-link">Raw API</a>
        </div>
        
        <div class="quick-stats" id="quick-stats">
            <!-- Dynamic content will be loaded here -->
        </div>
        
        <div class="api-info">
            <h3>Available API Endpoints</h3>
            <div class="endpoint">GET /api/v1/netmap/records - List all records</div>
            <div class="endpoint">GET /api/v1/netmap/records/count - Get count with filters</div>
            <div class="endpoint">GET /api/v1/netmap/records/total - Get total count</div>
            <div class="endpoint">GET /api/v1/netmap/exceptions - List exceptions</div>
            <div class="endpoint">POST /api/v1/netmap/records - Add new record</div>
            <div class="endpoint">POST /api/v1/netmap/netstat - Submit netstat data</div>
            <div class="endpoint">GET /metrics - Prometheus metrics</div>
            <div class="endpoint">GET /-/healthy - Health check</div>
        </div>
    </div>
    
    <footer>
        <p>Netmap Dashboard v1.0 • All connections are monitored</p>
    </footer>
    
    <script>
        // Load stats on page load
        document.addEventListener('DOMContentLoaded', function() {
            loadTotalCount();
            loadQuickStats();
            checkApiStatus();
        });
        
        function loadTotalCount() {
            fetch('/api/v1/netmap/records/total')
                .then(response => response.json())
                .then(data => {
                    document.getElementById('total-connections').textContent = 
                        data.total_count.toLocaleString();
                })
                .catch(error => {
                    console.error('Error loading total count:', error);
                    document.getElementById('total-connections').textContent = 'Error';
                });
        }
        
        function loadQuickStats() {
            // Get today's timestamp (start of day)
            const today = new Date();
            today.setHours(0, 0, 0, 0);
            const todayTs = Math.floor(today.getTime() / 1000);
            
            // Load multiple stats in parallel
            Promise.all([
                fetch('/api/v1/netmap/records/count?options_status=netmap').then(r => r.json()),
                fetch('/api/v1/netmap/records/count?timestamp=' + todayTs).then(r => r.json()),
                fetch('/api/v1/netmap/records?limit=100').then(r => r.json())
            ]).then(([activeData, todayData, recentData]) => {
                // Update active services count
                document.getElementById('active-services').textContent = 
                    activeData.count.toLocaleString();
                
                // Update today's records
                document.getElementById('today-records').textContent = 
                    todayData.count.toLocaleString();
                
                // Extract unique services from recent data
                if (recentData.status === 'success') {
                    const services = new Set();
                    recentData.data.forEach(record => {
                        if (record.options && record.options.service) {
                            services.add(record.options.service);
                        }
                    });
                    
                    // Update quick stats section
                    const quickStatsDiv = document.getElementById('quick-stats');
                    quickStatsDiv.innerHTML = 
                        '<div class="quick-stat">' +
                        '    <div class="value">' + services.size + '</div>' +
                        '    <div class="label">Unique Services</div>' +
                        '</div>' +
                        '<div class="quick-stat">' +
                        '    <div class="value">' + recentData.data.length + '</div>' +
                        '    <div class="label">Recent Records</div>' +
                        '</div>' +
                        '<div class="quick-stat">' +
                        '    <div class="value">' + activeData.count + '</div>' +
                        '    <div class="label">Active Now</div>' +
                        '</div>' +
                        '<div class="quick-stat">' +
                        '    <div class="value">' + todayData.count + '</div>' +
                        '    <div class="label">Today</div>' +
                        '</div>';
                }
            }).catch(error => {
                console.error('Error loading quick stats:', error);
            });
        }
        
        function checkApiStatus() {
            fetch('/-/healthy')
                .then(response => {
                    if (response.ok) {
                        document.getElementById('api-status').textContent = 'Online';
                        document.getElementById('api-status').style.color = '#4CAF50';
                    } else {
                        document.getElementById('api-status').textContent = '✗ Offline';
                        document.getElementById('api-status').style.color = '#f44336';
                    }
                })
                .catch(error => {
                    document.getElementById('api-status').textContent = 'Error';
                    document.getElementById('api-status').style.color = '#f44336';
                });
        }
        
        // Auto-refresh stats every 30 seconds
        setInterval(() => {
            loadTotalCount();
            loadQuickStats();
            checkApiStatus();
        }, 30000);
    </script>
</body>
</html>`

	w.Write([]byte(html))
}

func (api *Api) ApiRecordsPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	var args config.RecArgs
	for k, v := range r.URL.Query() {
		switch k {
		case "src_name":
			args.SrcName = v[0]
		case "type":
			args.Type = v[0]
		case "timestamp":
			args.Timestamp = v[0]
		case "relation_port":
			args.RelationPort = v[0]
		case "relation_type":
			args.RelationType = v[0]
		case "options_service":
			args.OptionsService = v[0]
		case "options_status":
			args.OptionsStatus = v[0]
		case "local_addr_ip":
			args.LocalAddrIp = v[0]
		case "remote_addr_ip":
			args.RemoteAddrIp = v[0]
		}
	}

	items, err := db.DbClient.LoadRecords(*api.DB, args)
	if err != nil {
		w.Write([]byte("<h1>Error loading records</h1><p>" + err.Error() + "</p>"))
		return
	}

	count := len(items)

	filtersHTML := ""
	if args.SrcName != "" {
		filtersHTML += `<span class="filter-tag">Src: ` + args.SrcName + `</span>`
	}
	if args.Type != "" {
		filtersHTML += `<span class="filter-tag">Type: ` + args.Type + `</span>`
	}
	if args.OptionsStatus != "" {
		filtersHTML += `<span class="filter-tag">Status: ` + args.OptionsStatus + `</span>`
	}
	if args.RelationType != "" {
		filtersHTML += `<span class="filter-tag">Protocol: ` + args.RelationMode + `</span>`
	}

	html := `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Netmap Records</title>
    <style>
        * {
            margin: 0;
            padding: 0;
            box-sizing: border-box;
            font-family: 'Segoe UI', Tahoma, Geneva, Verdana, sans-serif;
        }
        
        body {
            background-color: #f5f5f5;
            color: #333;
        }
        
        .container {
            max-width: 1600px;
            margin: 0 auto;
            padding: 20px;
        }
        
        header {
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            color: white;
            padding: 2rem 0;
            margin-bottom: 2rem;
        }
        
        header .container {
            display: flex;
            justify-content: space-between;
            align-items: center;
            flex-wrap: wrap;
        }
        
        header h1 {
            font-size: 2rem;
        }
        
        .back-button {
            display: inline-block;
            padding: 0.6rem 1.2rem;
            background: rgba(255, 255, 255, 0.2);
            color: white;
            text-decoration: none;
            border-radius: 6px;
            transition: background 0.3s ease;
        }
        
        .back-button:hover {
            background: rgba(255, 255, 255, 0.3);
        }
        
        .filters-bar {
            background: white;
            padding: 1.5rem;
            border-radius: 10px;
            box-shadow: 0 2px 4px rgba(0, 0, 0, 0.1);
            margin-bottom: 1.5rem;
        }
        
        .filter-tag {
            display: inline-block;
            background: #e3f2fd;
            color: #1976d2;
            padding: 0.4rem 0.8rem;
            border-radius: 4px;
            margin-right: 0.5rem;
            margin-bottom: 0.5rem;
            font-size: 0.9rem;
        }
        
        .records-count {
            font-size: 1.1rem;
            margin-bottom: 1rem;
            color: #666;
        }
        
        .records-count strong {
            color: #667eea;
        }
        
        table {
            width: 100%;
            background: white;
            border-radius: 10px;
            overflow: hidden;
            box-shadow: 0 2px 4px rgba(0, 0, 0, 0.1);
            margin-bottom: 2rem;
            border-collapse: collapse;
        }
        
        th {
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            color: white;
            padding: 1rem;
            text-align: left;
            font-weight: 600;
            position: sticky;
            top: 0;
        }
        
        td {
            padding: 1rem;
            border-bottom: 1px solid #eee;
        }
        
        tr:hover {
            background-color: #f9f9f9;
        }
        
        .status-active {
            color: #4CAF50;
            font-weight: 600;
        }
        
        .status-inactive {
            color: #f44336;
            font-weight: 600;
        }
        
        .protocol-tcp {
            color: #2196F3;
        }
        
        .protocol-udp {
            color: #FF9800;
        }
        
        .ip-address {
            font-family: 'Courier New', monospace;
            font-size: 0.9rem;
            color: #555;
        }
        
        .timestamp {
            font-size: 0.9rem;
            color: #888;
        }
        
        .pagination {
            display: flex;
            justify-content: center;
            gap: 0.5rem;
            margin-top: 2rem;
        }
        
        .pagination button {
            padding: 0.5rem 1rem;
            border: 1px solid #ddd;
            background: white;
            border-radius: 4px;
            cursor: pointer;
            transition: all 0.3s ease;
        }
        
        .pagination button:hover {
            background: #667eea;
            color: white;
            border-color: #667eea;
        }
        
        .export-buttons {
            margin-bottom: 1rem;
            display: flex;
            gap: 1rem;
        }
        
        .export-button {
            padding: 0.5rem 1rem;
            background: #4CAF50;
            color: white;
            border: none;
            border-radius: 4px;
            cursor: pointer;
            text-decoration: none;
            display: inline-block;
        }
        
        @media (max-width: 1200px) {
            table {
                display: block;
                overflow-x: auto;
            }
            
            th, td {
                min-width: 150px;
            }
        }
        
        .refresh-button {
            background: #4CAF50;
            color: white;
            border: none;
            padding: 0.8rem 1.5rem;
            border-radius: 6px;
            cursor: pointer;
            font-size: 1rem;
            margin-left: 1rem;
        }
        
        .auto-refresh {
            display: flex;
            align-items: center;
            gap: 0.5rem;
            margin-top: 1rem;
        }
    </style>
</head>
<body>
    <header>
        <div class="container">
            <h1>Network Connections</h1>
            <a href="/" class="back-button">← Back to Dashboard</a>
        </div>
    </header>
    
    <div class="container">
        <div class="filters-bar">
            <div class="records-count">
                Showing <strong>` + strconv.Itoa(count) + `</strong> records`

	if filtersHTML != "" {
		html += ` with filters: <div style="margin-top: 0.5rem;">` + filtersHTML + `</div>`
	}

	html += `
            </div>
            
            <div class="export-buttons">
                <a href="/api/v1/netmap/records?format=json" class="export-button" target="_blank">Export JSON</a>
                <button class="export-button" onclick="exportToCSV()">Export CSV</button>
            </div>
            
            <div class="auto-refresh">
                <label>
                    <input type="checkbox" id="autoRefresh" onchange="toggleAutoRefresh()">
                    Auto-refresh every 30 seconds
                </label>
                <button class="refresh-button" onclick="location.reload()">Refresh Now</button>
            </div>
        </div>
        
        <table id="recordsTable">
            <thead>
                <tr>
                    <th>Timestamp</th>
                    <th>Local Address</th>
                    <th>Remote Address</th>
                    <th>Mode</th>
                    <th>Port</th>
                    <th>Service</th>
                    <th>Status</th>
                    <th>Result</th>
                    <th>Response Time</th>
                </tr>
            </thead>
            <tbody>`

	for _, item := range items {
		timeStr := time.Unix(item.Timestamp, 0).Format("2006-01-02 15:04:05")

		statusClass := "status-inactive"
		if item.Options.Status == "active" {
			statusClass = "status-active"
		}

		protocolClass := ""
		if item.Relation.Mode == "tcp" {
			protocolClass = "protocol-tcp"
		} else if item.Relation.Mode == "udp" {
			protocolClass = "protocol-udp"
		}

		html += `<tr>
            <td class="timestamp">` + timeStr + `</td>
            <td>
                <div><strong>` + item.LocalAddr.Name + `</strong></div>
                <div class="ip-address">` + item.LocalAddr.IP + `</div>
            </td>
            <td>
                <div><strong>` + item.RemoteAddr.Name + `</strong></div>
                <div class="ip-address">` + item.RemoteAddr.IP + `</div>
            </td>
            <td class="` + protocolClass + `">` + strings.ToUpper(item.Relation.Mode) + `</td>
            <td>` + strconv.FormatUint(uint64(item.Relation.Port), 10) + `</td>
            <td>` + item.Options.Service + `</td>
            <td class="` + statusClass + `">` + strings.ToUpper(item.Options.Status) + `</td>
            <td>` + strconv.FormatInt(int64(item.Relation.Result), 10) + `</td>
            <td>` + fmt.Sprintf("%.2f", item.Relation.Response) + `ms</td>
        </tr>`
	}

	html += `</tbody>
        </table>
        
        <div class="pagination" id="pagination">
            <!-- Pagination will be added by JavaScript -->
        </div>
    </div>
    
    <script>
        // Export to CSV functionality
        function exportToCSV() {
            const rows = document.querySelectorAll('#recordsTable tbody tr');
            let csv = 'Timestamp,Local Name,Local IP,Remote Name,Remote IP,Protocol,Port,Service,Status,Result,Response Time\\n';
            
            rows.forEach(row => {
                const cells = row.querySelectorAll('td');
                const rowData = Array.from(cells).map(cell => {
                    let text = cell.textContent.trim();
                    // Remove extra whitespace and newlines
                    text = text.replace(/\\s+/g, ' ');
                    // Escape quotes
                    text = text.replace(/"/g, '""');
                    // Wrap in quotes if contains comma
                    return text.includes(',') ? '"' + text + '"' : text;
                });
                csv += rowData.join(',') + '\\n';
            });
            
            const blob = new Blob([csv], { type: 'text/csv' });
            const url = window.URL.createObjectURL(blob);
            const a = document.createElement('a');
            a.href = url;
            a.download = 'netmap-records-' + new Date().toISOString().slice(0, 10) + '.csv';
            document.body.appendChild(a);
            a.click();
            document.body.removeChild(a);
            window.URL.revokeObjectURL(url);
        }
        
        // Auto-refresh functionality
        let refreshInterval;
        
        function toggleAutoRefresh() {
            const checkbox = document.getElementById('autoRefresh');
            if (checkbox.checked) {
                refreshInterval = setInterval(() => {
                    location.reload();
                }, 30000);
            } else {
                if (refreshInterval) {
                    clearInterval(refreshInterval);
                }
            }
        }
        
        // Initialize pagination if there are many records
        function initPagination() {
            const rows = document.querySelectorAll('#recordsTable tbody tr');
            if (rows.length > 100) {
                const paginationDiv = document.getElementById('pagination');
                const pageSize = 100;
                const pageCount = Math.ceil(rows.length / pageSize);
                
                for (let i = 0; i < pageCount; i++) {
                    const button = document.createElement('button');
                    button.textContent = i + 1;
                    button.onclick = () => showPage(i + 1);
                    paginationDiv.appendChild(button);
                }
                
                showPage(1);
            }
        }
        
        function showPage(pageNum) {
            const rows = document.querySelectorAll('#recordsTable tbody tr');
            const pageSize = 100;
            const start = (pageNum - 1) * pageSize;
            const end = start + pageSize;
            
            rows.forEach((row, index) => {
                if (index >= start && index < end) {
                    row.style.display = '';
                } else {
                    row.style.display = 'none';
                }
            });
        }
        
        // Initialize on load
        document.addEventListener('DOMContentLoaded', initPagination);
    </script>
</body>
</html>`

	w.Write([]byte(html))
}

func (api *Api) ApiSearchPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	html := `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Advanced Search - Netmap</title>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; font-family: 'Segoe UI', sans-serif; }
        body { background-color: #f5f5f5; color: #333; }
        .container { max-width: 1200px; margin: 0 auto; padding: 20px; }
        header { background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); color: white; padding: 2rem 0; margin-bottom: 2rem; }
        header .container { display: flex; justify-content: space-between; align-items: center; flex-wrap: wrap; }
        header h1 { font-size: 2rem; }
        .back-button { display: inline-block; padding: 0.6rem 1.2rem; background: rgba(255, 255, 255, 0.2); color: white; text-decoration: none; border-radius: 6px; }
        .search-form { background: white; padding: 2rem; border-radius: 10px; box-shadow: 0 2px 4px rgba(0, 0, 0, 0.1); margin-bottom: 2rem; }
        .form-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(300px, 1fr)); gap: 1.5rem; margin-bottom: 1.5rem; }
        .form-group { display: flex; flex-direction: column; }
        .form-group label { margin-bottom: 0.5rem; font-weight: 600; color: #555; }
        .form-group input, .form-group select { padding: 0.8rem; border: 1px solid #ddd; border-radius: 6px; font-size: 1rem; }
        .form-actions { display: flex; gap: 1rem; justify-content: flex-end; margin-top: 1.5rem; }
        .btn { padding: 0.8rem 1.5rem; border: none; border-radius: 6px; font-size: 1rem; cursor: pointer; }
        .btn-primary { background: #667eea; color: white; }
        .btn-secondary { background: #6c757d; color: white; }
        .btn-reset { background: #f8f9fa; color: #333; border: 1px solid #ddd; }
        .search-results { background: white; padding: 2rem; border-radius: 10px; box-shadow: 0 2px 4px rgba(0, 0, 0, 0.1); display: none; }
        .results-table { width: 100%; border-collapse: collapse; margin-top: 1rem; }
        .results-table th { background: #f8f9fa; padding: 1rem; text-align: left; border-bottom: 2px solid #dee2e6; }
        .results-table td { padding: 0.8rem 1rem; border-bottom: 1px solid #eee; }
        .results-table tr:hover { background: #f8f9fa; }
        .loading { display: none; text-align: center; padding: 2rem; color: #667eea; }
        .loading-spinner { border: 3px solid #f3f3f3; border-top: 3px solid #667eea; border-radius: 50%; width: 40px; height: 40px; animation: spin 1s linear infinite; margin: 0 auto 1rem; }
        @keyframes spin { 0% { transform: rotate(0deg); } 100% { transform: rotate(360deg); } }
        .results-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 1rem; }
        .results-count { font-size: 1.2rem; color: #667eea; font-weight: 600; }
        .status-active { color: #4CAF50; font-weight: 600; }
        .status-inactive { color: #f44336; font-weight: 600; }
        .protocol-tcp { color: #2196F3; }
        .protocol-udp { color: #FF9800; }
        .ip-address { font-family: 'Courier New', monospace; font-size: 0.9rem; color: #555; }
    </style>
</head>
<body>
    <header>
        <div class="container">
            <h1>Advanced Search</h1>
            <a href="/" class="back-button">← Back to Dashboard</a>
        </div>
    </header>
    
    <div class="container">
        <div class="search-form">
            <h2 style="margin-bottom: 1.5rem; color: #333;">Search Filters</h2>
            
            <div class="form-grid">
                <div class="form-group">
                    <label for="src_name">Source Name</label>
                    <input type="text" id="src_name" placeholder="e.g., server-01">
                </div>
                
                <div class="form-group">
                    <label for="local_addr_ip">Local IP Address</label>
                    <input type="text" id="local_addr_ip" placeholder="e.g., 192.168.1.1">
                </div>
                
                <div class="form-group">
                    <label for="remote_addr_ip">Remote IP Address</label>
                    <input type="text" id="remote_addr_ip" placeholder="e.g., 10.0.0.1">
                </div>
                
                <div class="form-group">
                    <label for="relation_port">Port Number</label>
                    <input type="number" id="relation_port" placeholder="e.g., 443">
                </div>
                
                <div class="form-group">
                    <label for="relation_mode">Protocol</label>
                    <select id="relation_mode">
                        <option value="">Any</option>
                        <option value="tcp">TCP</option>
                        <option value="udp">UDP</option>
                    </select>
                </div>
                
                <div class="form-group">
                    <label for="options_service">Service</label>
                    <input type="text" id="options_service" placeholder="e.g., http, ssh, dns">
                </div>
                
                <div class="form-group">
                    <label for="options_status">Status</label>
                    <select id="options_status">
                        <option value="">Any</option>
                        <option value="active">Active</option>
                        <option value="inactive">Inactive</option>
                    </select>
                </div>
                
                <div class="form-group">
                    <label for="timestamp">Timestamp (>=)</label>
                    <input type="number" id="timestamp" placeholder="Unix timestamp">
                </div>
            </div>
            
            <div class="form-actions">
                <button class="btn btn-reset" onclick="resetForm()">Reset</button>
                <button class="btn btn-secondary" onclick="getCount()">Get Count</button>
                <button class="btn btn-primary" onclick="searchRecords()">Search Records</button>
            </div>
        </div>
        
        <div class="loading" id="loading">
            <div class="loading-spinner"></div>
            <p>Searching records...</p>
        </div>
        
        <div class="search-results" id="searchResults">
            <div class="results-header">
                <div class="results-count" id="resultsCount">0 records found</div>
                <button class="btn btn-primary" onclick="exportResults()">Export Results</button>
            </div>
            <div id="resultsTableContainer"></div>
        </div>
    </div>
    
    <script>
        let currentResults = [];
        
        function resetForm() {
            document.querySelectorAll('input, select').forEach(element => {
                if (element.type !== 'button') {
                    element.value = '';
                }
            });
        }
        
        function buildQueryString() {
            const params = new URLSearchParams();
            const fields = [
                'src_name', 'local_addr_ip', 'remote_addr_ip', 'relation_port',
                'relation_mode', 'options_service', 'options_status', 'timestamp'
            ];
            
            fields.forEach(field => {
                const value = document.getElementById(field).value;
                if (value) {
                    params.append(field, value);
                }
            });
            
            return params.toString();
        }
        
        function getCount() {
            const query = buildQueryString();
            
            fetch('/api/v1/netmap/records/count?' + query)
                .then(response => {
                    if (!response.ok) {
                        throw new Error('Network response was not ok');
                    }
                    return response.json();
                })
                .then(data => {
                    alert('Found ' + data.count + ' records with current filters');
                })
                .catch(error => {
                    alert('Failed to get count: ' + error.message);
                    console.error('Error:', error);
                });
        }
        
        function searchRecords() {
            const query = buildQueryString();
            const loading = document.getElementById('loading');
            const resultsDiv = document.getElementById('searchResults');
            
            loading.style.display = 'block';
            resultsDiv.style.display = 'none';
            
            fetch('/api/v1/netmap/records?' + query)
                .then(response => {
                    if (!response.ok) {
                        throw new Error('Network response was not ok');
                    }
                    return response.json();
                })
                .then(data => {
                    loading.style.display = 'none';
                    if (data.status === 'success') {
                        currentResults = data.data;
                        displayResults(data.data);
                        resultsDiv.style.display = 'block';
                    } else {
                        alert('Error: ' + (data.error || 'Unknown error'));
                    }
                })
                .catch(error => {
                    loading.style.display = 'none';
                    alert('Search failed: ' + error.message);
                    console.error('Error:', error);
                });
        }
        
        function displayResults(results) {
            const container = document.getElementById('resultsTableContainer');
            const countSpan = document.getElementById('resultsCount');
            
            countSpan.textContent = results.length + ' records found';
            
            if (results.length === 0) {
                container.innerHTML = '<p style="text-align: center; padding: 2rem;">No records found</p>';
                return;
            }
            
            let html = '<table class="results-table">';
            html += '<thead><tr>';
            html += '<th>Time</th><th>Local</th><th>Remote</th><th>Protocol</th><th>Port</th><th>Service</th><th>Status</th>';
            html += '</tr></thead><tbody>';
            
            results.forEach(record => {
                // Format timestamp
                const date = new Date(record.timestamp * 1000);
                const timeStr = date.toLocaleDateString() + ' ' + date.toLocaleTimeString();
                
                // Determine status class
                const statusClass = record.options && record.options.status === 'active' ? 'status-active' : 'status-inactive';
                const statusText = record.options && record.options.status ? record.options.status.toUpperCase() : 'UNKNOWN';
                
                // Determine protocol class
                const protocolClass = record.relation && record.relation.type === 'tcp' ? 'protocol-tcp' : 
                                     record.relation && record.relation.type === 'udp' ? 'protocol-udp' : '';
                const protocolText = record.relation && record.relation.type ? record.relation.type.toUpperCase() : '';
                
                html += '<tr>';
                html += '<td>' + timeStr + '</td>';
                html += '<td>' + 
                    (record.localAddr ? record.localAddr.name : 'N/A') + 
                    '<br><small class="ip-address">' + 
                    (record.localAddr ? record.localAddr.ip : 'N/A') + 
                    '</small></td>';
                html += '<td>' + 
                    (record.remoteAddr ? record.remoteAddr.name : 'N/A') + 
                    '<br><small class="ip-address">' + 
                    (record.remoteAddr ? record.remoteAddr.ip : 'N/A') + 
                    '</small></td>';
                html += '<td class="' + protocolClass + '">' + protocolText + '</td>';
                html += '<td>' + (record.relation ? record.relation.port : '') + '</td>';
                html += '<td>' + (record.options ? record.options.service : '') + '</td>';
                html += '<td class="' + statusClass + '">' + statusText + '</td>';
                html += '</tr>';
            });
            
            html += '</tbody></table>';
            container.innerHTML = html;
        }
        
        function exportResults() {
            if (currentResults.length === 0) {
                alert('No results to export');
                return;
            }
            
            // Create CSV content
            const headers = ['Timestamp', 'Local Name', 'Local IP', 'Remote Name', 'Remote IP', 
                           'Protocol', 'Port', 'Service', 'Status', 'Result', 'Response Time'];
            
            const rows = currentResults.map(record => {
                const date = new Date(record.timestamp * 1000);
                return [
                    date.toISOString(),
                    record.localAddr ? record.localAddr.name : '',
                    record.localAddr ? record.localAddr.ip : '',
                    record.remoteAddr ? record.remoteAddr.name : '',
                    record.remoteAddr ? record.remoteAddr.ip : '',
                    record.relation ? record.relation.type : '',
                    record.relation ? record.relation.port : '',
                    record.options ? record.options.service : '',
                    record.options ? record.options.status : '',
                    record.relation ? record.relation.result : '',
                    record.relation ? record.relation.response : ''
                ];
            });
            
            const csvContent = [
                headers.join(','),
                ...rows.map(row => row.map(cell => 
                    typeof cell === 'string' && cell.includes(',') ? '"' + cell + '"' : cell
                ).join(','))
            ].join('\\n');
            
            // Create download link
            const blob = new Blob([csvContent], { type: 'text/csv' });
            const url = window.URL.createObjectURL(blob);
            const a = document.createElement('a');
            a.href = url;
            a.download = 'search-results-' + new Date().toISOString().slice(0, 10) + '.csv';
            document.body.appendChild(a);
            a.click();
            document.body.removeChild(a);
            window.URL.revokeObjectURL(url);
        }
        
        // Add event listeners for Enter key in input fields
        document.addEventListener('DOMContentLoaded', function() {
            const inputs = document.querySelectorAll('input');
            inputs.forEach(input => {
                input.addEventListener('keypress', function(e) {
                    if (e.key === 'Enter') {
                        searchRecords();
                    }
                });
            });
        });
    </script>
</body>
</html>`

	w.Write([]byte(html))
}
