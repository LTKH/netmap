package v1

import (
    "log"
    "fmt"
    "sync"
    "strconv"
    "net/http"
    "time"
    "compress/gzip"
    "io"
    "bytes"
    "regexp"
    //"context"
    "io/ioutil"
    "encoding/json"
    "github.com/prometheus/client_golang/prometheus"
    "github.com/ltkh/netmap/internal/db"
    "github.com/ltkh/netmap/internal/config"
    "github.com/ltkh/netmap/internal/client"

    //"google.golang.org/grpc"
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
        []string{"src_name","dst_name","mode","port"},
    )

    responseTime = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Namespace: "netmap",
            Name:      "response_time",
            Help:      "",
        },
        []string{"src_name","dst_name","mode","port"},
    )
)

type Api struct {
    Conf         *config.Config            `json:"conf"`
    DB           *db.DbClient              `json:"db"`
    Collect      chan config.SockTable     `json:"-"`
    Server       *Server                   `json:"-"`
}

type Resp struct {
    Status       string                    `json:"status"`
    Error        string                    `json:"error,omitempty"`
    Warnings     []string                  `json:"warnings,omitempty"`
    Data         []interface{}             `json:"data"`
}

type Records struct {
    sync.RWMutex
    items        map[string]config.SockTable
}

type Exceptions struct {
    sync.RWMutex
    items        map[string]config.Exception
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
        return encodeResp(&Resp{Status:"error", Error:err.Error(), Data:make([]interface{}, 0)})
    }
    return jsn
}

func compressData(data []byte, encoding string) (bytes.Buffer, bool, error) {
    var buf bytes.Buffer
    // Send compressed data if needed
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

func MonRegister(){
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
        Conf: conf,
        DB: &db,
        Collect: make(chan config.SockTable, 1000000),
        Server: srv,
    }

    rpc := &Rpc{
        Debug: debug,
        Peers: peers,
        DB: &db,
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
                URLs: api.Conf.Collector.URLs,
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
            w.Write(encodeResp(&Resp{Status:"error", Error:"access is denied"}))
            return
        }
    }

    if r.Method == "POST" {
        var reader io.ReadCloser
        var err error

        // Check that the server actual sent compressed data
        switch r.Header.Get("Content-Encoding") {
            case "gzip":
                reader, err = gzip.NewReader(r.Body)
                if err != nil {
                    log.Printf("[error] %v - %s", err, r.URL.Path)
                    w.WriteHeader(400)
                    w.Write(encodeResp(&Resp{Status:"error", Error:err.Error()}))
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
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error()}))
            return
        }

        var netstat config.NetstatData

        if err := json.Unmarshal(body, &netstat); err != nil {
            log.Printf("[error] %v - %s", err, r.URL.Path)
            w.WriteHeader(400)
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error()}))
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
                w.Write(encodeResp(&Resp{Status:"error", Error:err.Error()}))
                return
            }

            event := convertRec(ServerId, "setStatus", rc)
            api.Server.Broadcast(event)
        }

        w.WriteHeader(204)
        return
    }

    w.WriteHeader(405)
    w.Write(encodeResp(&Resp{Status:"error", Error:"method not allowed"}))
}

func (api *Api) ApiNetstat(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")

    user, pass, _ := r.BasicAuth()
    if len(api.Conf.Global.Users) > 0 {
        if ps, ok := api.Conf.Global.Users[user]; !ok || ps != pass {
            w.WriteHeader(403)
            w.Write(encodeResp(&Resp{Status:"error", Error:"access is denied"}))
            return
        }
    }

    if r.Method == "POST" {
        var reader io.ReadCloser
        var err error

        // Check that the server actual sent compressed data
        switch r.Header.Get("Content-Encoding") {
            case "gzip":
                reader, err = gzip.NewReader(r.Body)
                if err != nil {
                    log.Printf("[error] %v - %s", err, r.URL.Path)
                    w.WriteHeader(400)
                    w.Write(encodeResp(&Resp{Status:"error", Error:err.Error()}))
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
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error()}))
            return
        }

        var netstat config.NetstatData

        if err := json.Unmarshal(body, &netstat); err != nil {
            log.Printf("[error] %v - %s", err, r.URL.Path)
            w.WriteHeader(400)
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error()}))
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
                    //log.Printf("[debug] len chan - %v", len(api.Collect))
                default: 
                    // Канал переполнен, можно удалить или игнорировать
                }
            }
        }
        
        w.WriteHeader(204)
        return
    }

    w.WriteHeader(405)
    w.Write(encodeResp(&Resp{Status:"error", Error:"method not allowed"}))
}

func (api *Api) ApiTracert(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")

    user, pass, _ := r.BasicAuth()
    if len(api.Conf.Global.Users) > 0 {
        if ps, ok := api.Conf.Global.Users[user]; !ok || ps != pass {
            w.WriteHeader(403)
            w.Write(encodeResp(&Resp{Status:"error", Error:"access is denied"}))
            return
        }
    }

    if r.Method == "POST" {
        var reader io.ReadCloser
        var err error

        // Check that the server actual sent compressed data
        switch r.Header.Get("Content-Encoding") {
            case "gzip":
                reader, err = gzip.NewReader(r.Body)
                if err != nil {
                    log.Printf("[error] %v - %s", err, r.URL.Path)
                    w.WriteHeader(400)
                    w.Write(encodeResp(&Resp{Status:"error", Error:err.Error()}))
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
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error()}))
            return
        }

        var netstat config.NetstatData

        if err := json.Unmarshal(body, &netstat); err != nil {
            log.Printf("[error] %v - %s", err, r.URL.Path)
            w.WriteHeader(400)
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error()}))
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
                w.Write(encodeResp(&Resp{Status:"error", Error:err.Error()}))
                return
            }

            event := convertRec(ServerId, "setTracert", rc)
            api.Server.Broadcast(event)
        }
        
        w.WriteHeader(204)
        return
    }

    w.WriteHeader(405)
    w.Write(encodeResp(&Resp{Status:"error", Error:"method not allowed"}))
}

func (api *Api) ApiRecords(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")

    user, pass, _ := r.BasicAuth()
    if len(api.Conf.Global.Users) > 0 {
        if ps, ok := api.Conf.Global.Users[user]; !ok || ps != pass {
            w.WriteHeader(403)
            w.Write(encodeResp(&Resp{Status:"error", Error:"access is denied"}))
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
                    i, err := strconv.Atoi(v[0])
                    if err != nil {
                        w.WriteHeader(400)
                        w.Write(encodeResp(&Resp{Status:"error", Error:fmt.Sprintf("executing query: invalid parameter: %v", k)}))
                        return
                    }
                    args.Timestamp = int64(i)
            }
        }

        items, err := db.DbClient.LoadRecords(*api.DB, args)
        if err != nil {
            log.Printf("[error] %v - %s", err, r.URL.Path)
            w.WriteHeader(500)
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error()}))
            return
        }

        var records []interface{}
        for _, item := range items{
            if item.Timestamp < args.Timestamp {
                continue
            }
            records = append(records, item)
        }

        data := encodeResp(&Resp{Status:"success", Data:records})

        w.WriteHeader(200)
        w.Write(data)
        return
    }

    if r.Method == "POST" {
        var reader io.ReadCloser
        var err error

        // Check that the server actual sent compressed data
        switch r.Header.Get("Content-Encoding") {
            case "gzip":
                reader, err = gzip.NewReader(r.Body)
                if err != nil {
                    log.Printf("[error] %v - %s", err, r.URL.Path)
                    w.WriteHeader(400)
                    w.Write(encodeResp(&Resp{Status:"error", Error:err.Error()}))
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
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error()}))
            return
        }

        var recData config.RecordsData

        if err := json.Unmarshal(body, &recData); err != nil {
            log.Printf("[error] %v - %s", err, r.URL.Path)
            w.WriteHeader(400)
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error()}))
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
            //log.Printf("%v", string(jsonRC))
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

        // Check that the server actual sent compressed data
        switch r.Header.Get("Content-Encoding") {
            case "gzip":
                reader, err = gzip.NewReader(r.Body)
                if err != nil {
                    log.Printf("[error] %v - %s", err, r.URL.Path)
                    w.WriteHeader(400)
                    w.Write(encodeResp(&Resp{Status:"error", Error:err.Error()}))
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
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error()}))
            return
        }

        var keys []string

        if err := json.Unmarshal(body, &keys); err != nil {
            log.Printf("[error] %v - %s", err, r.URL.Path)
            w.WriteHeader(400)
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error()}))
            return
        }

        for _, id := range keys {
            if err := db.DbClient.DelRecord(*api.DB, id); err != nil {
                w.WriteHeader(500)
                w.Write(encodeResp(&Resp{Status:"error", Error:err.Error()}))
                return
            }

            event := &pb.Event{
                ServerId:        ServerId,
                Event:           "delRecord",
                Id:              id,
            }
            api.Server.Broadcast(event)
        }

        w.WriteHeader(200)
        w.Write(encodeResp(&Resp{Status:"success"}))
        return
    }

    w.WriteHeader(405)
    w.Write(encodeResp(&Resp{Status:"error", Error:"method not allowed"}))
}

func (api *Api) ApiExceptions(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")

    user, pass, _ := r.BasicAuth()
    if len(api.Conf.Global.Users) > 0 {
        if ps, ok := api.Conf.Global.Users[user]; !ok || ps != pass {
            w.WriteHeader(403)
            w.Write(encodeResp(&Resp{Status:"error", Error:"access is denied"}))
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
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error()}))
            return
        }

        var exceptions []interface{}
        for _, item := range items{
            exceptions = append(exceptions, item)
        }

        data := encodeResp(&Resp{Status:"success", Data:exceptions})
        buf, ok, err := compressData(data, r.Header.Get("Accept-Encoding"))
        if err != nil {
            log.Printf("[error] %v - %s", err, r.URL.Path)
            w.WriteHeader(500)
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error()}))
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

        // Check that the server actual sent compressed data
        switch r.Header.Get("Content-Encoding") {
            case "gzip":
                reader, err = gzip.NewReader(r.Body)
                if err != nil {
                    log.Printf("[error] %v - %s", err, r.URL.Path)
                    w.WriteHeader(400)
                    w.Write(encodeResp(&Resp{Status:"error", Error:err.Error()}))
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
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error()}))
            return
        }

        var expdata config.ExceptionData

        if err := json.Unmarshal(body, &expdata); err != nil {
            log.Printf("[error] %v - %s", err, r.URL.Path)
            w.WriteHeader(400)
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error()}))
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
                Id:              ex.Id,
                Timestamp:       ex.Timestamp,
                Options: config.Options{
                    AccountID:   ex.AccountID,
                    HostMask:    ex.HostMask,
                    IgnoreMask:  ex.IgnoreMask,
                },
            }

            if err := db.DbClient.SaveException(*api.DB, rc); err != nil {
                w.WriteHeader(500)
                w.Write(encodeResp(&Resp{Status:"error", Error:err.Error()}))
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

        // Check that the server actual sent compressed data
        switch r.Header.Get("Content-Encoding") {
            case "gzip":
                reader, err = gzip.NewReader(r.Body)
                if err != nil {
                    log.Printf("[error] %v - %s", err, r.URL.Path)
                    w.WriteHeader(400)
                    w.Write(encodeResp(&Resp{Status:"error", Error:err.Error()}))
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
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error()}))
            return
        }

        var keys []string

        if err := json.Unmarshal(body, &keys); err != nil {
            log.Printf("[error] %v - %s", err, r.URL.Path)
            w.WriteHeader(400)
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error()}))
            return
        }

        for _, id := range keys {
            if err := db.DbClient.DelException(*api.DB, id); err != nil {
                w.WriteHeader(500)
                w.Write(encodeResp(&Resp{Status:"error", Error:err.Error()}))
                return
            }

            event := &pb.Event{
                ServerId:        ServerId,
                Event:           "delException",
                Id:              id,
            }
            api.Server.Broadcast(event)
        }

        w.WriteHeader(200)
        w.Write(encodeResp(&Resp{Status:"success"}))
        return
    }

    w.WriteHeader(405)
    w.Write(encodeResp(&Resp{Status:"error", Error:"method not allowed"}))
}

func (api *Api) ApiWebhook(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")

    user, pass, _ := r.BasicAuth()
    if len(api.Conf.Global.Users) > 0 {
        if ps, ok := api.Conf.Global.Users[user]; !ok || ps != pass {
            w.WriteHeader(403)
            w.Write(encodeResp(&Resp{Status:"error", Error:"access is denied"}))
            return
        }
    }

    if r.Method == "POST" {
        var reader io.ReadCloser
        var err error

        // Check that the server actual sent compressed data
        switch r.Header.Get("Content-Encoding") {
            case "gzip":
                reader, err = gzip.NewReader(r.Body)
                if err != nil {
                    log.Printf("[error] %v - %s", err, r.URL.Path)
                    w.WriteHeader(400)
                    w.Write(encodeResp(&Resp{Status:"error", Error:err.Error()}))
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
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error()}))
            return
        }

        if len(api.Conf.Notifier.URLs) > 0 {
            for _, url := range api.Conf.Notifier.URLs {
                config := client.HttpConfig{
                    URLs: []string{url},
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
    w.Write(encodeResp(&Resp{Status:"error", Error:"method not allowed"}))
}