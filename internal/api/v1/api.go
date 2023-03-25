package v1

import (
    "log"
    "fmt"
    "strconv"
    "net/http"
    "time"
    //"errors"
    "compress/gzip"
    "io"
    "bytes"
    "regexp"
    "io/ioutil"
    "encoding/json"
    "crypto/sha1"
    "encoding/hex"
    "github.com/prometheus/client_golang/prometheus"
    "github.com/ltkh/netmap/internal/config"
    "github.com/ltkh/netmap/internal/client"
    "github.com/ltkh/netmap/internal/db"
)

var (
    httpClient = client.NewHttpClient()

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
    conf         *config.Config
    db           *db.DbClient
}

type Resp struct {
    Status       string                    `json:"status"`
    Error        string                    `json:"error,omitempty"`
    Warnings     []string                  `json:"warnings,omitempty"`
    Data         interface{}               `json:"data"`
}

type NetstatData struct {
    Data         []config.SockTable        `json:"data"`
}

type ExceptionData struct {
    Data         []config.Exception        `json:"data"`
}

func getHash(text string) string {
    h := sha1.New()
    io.WriteString(h, text)
    return hex.EncodeToString(h.Sum(nil))
}

func getIdRec(i *config.SockTable) string {
    return getHash(fmt.Sprintf("%v:%v:%v:%v:%v:%v", i.LocalAddr.IP, i.RemoteAddr.IP, i.Relation.Mode, i.Relation.Port))
}

func getIdExp(i *config.Exception) string {
    return getHash(fmt.Sprintf("%v:%v", i, time.Now().UTC().Unix()))
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
    jsn, err := json.Marshal(resp)
    if err != nil {
        return encodeResp(&Resp{Status:"error", Error:err.Error(), Data:make([]int, 0)})
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

func readBody(r *http.Request) ([]byte, error) {
    var body []byte
    var reader io.ReadCloser

    // Check that the server actual sent compressed data
    switch r.Header.Get("Content-Encoding") {
        case "gzip":
            reader, err := gzip.NewReader(r.Body)
            if err != nil {
                return body, err
            }
            defer reader.Close()
        default:
            reader = r.Body
    }
    defer r.Body.Close()

    body, err := ioutil.ReadAll(reader)
    if err != nil {
        return body, err
    }

    return body, nil
}

func MonRegister(){
    prometheus.MustRegister(resultCode)
    prometheus.MustRegister(responseTime)
}

func New(conf *config.Config) (*Api, error) {

    client, err := db.NewClient(conf.DB)
    if err != nil {
        return &Api{}, err
    }

    if err := client.CreateTables(); err != nil {
        return &Api{}, err
    }

    return &Api{conf: conf, db: &client}, nil
}

func (api *Api) ApiStatus(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")

    if r.Method == "POST" {
        body, err := readBody(r)
        if err != nil {
            log.Printf("[error] %v - %s", err, r.URL.Path)
            w.WriteHeader(400)
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error(), Data:make([]int, 0)}))
            return
        }

        var netstat NetstatData

        if err := json.Unmarshal(body, &netstat); err != nil {
            log.Printf("[error] %v - %s", err, r.URL.Path)
            w.WriteHeader(400)
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error(), Data:make([]int, 0)}))
            return
        }

        if err := db.DbClient.SaveStatus(*api.db, netstat.Data); err != nil {
            log.Printf("[error] %v - %s", err, r.URL.Path)
            w.WriteHeader(400)
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error(), Data:make([]int, 0)}))
            return
        }
        
        w.WriteHeader(204)
        return
    }

    w.WriteHeader(405)
    w.Write(encodeResp(&Resp{Status:"error", Error:"method not allowed", Data:make([]int, 0)}))
}

func (api *Api) ApiNetstat(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(405)
    w.Write(encodeResp(&Resp{Status:"error", Error:"method not allowed", Data:make([]int, 0)}))
}

func (api *Api) ApiRecords(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")

    if r.Method == "GET" {

        args := config.RecArgs{}

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
                        w.Write(encodeResp(&Resp{Status:"error", Error:fmt.Sprintf("executing query: invalid parameter: %v", k), Data:make([]int, 0)}))
                        return
                    }
                    args.Timestamp = int64(i)
            }
        }

        records, err := db.DbClient.LoadRecords(*api.db, args)
        if err != nil {
            log.Printf("[error] %v - %s", err, r.URL.Path)
            w.WriteHeader(400)
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error(), Data:make([]int, 0)}))
            return
        }

        data := encodeResp(&Resp{Status:"success", Data:records})
        buf, ok, err := compressData(data, r.Header.Get("Accept-Encoding"))
        if err != nil {
            log.Printf("[error] %v - %s", err, r.URL.Path)
            w.WriteHeader(500)
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error(), Data:make([]int, 0)}))
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
        body, err := readBody(r)
        if err != nil {
            log.Printf("[error] %v - %s", err, r.URL.Path)
            w.WriteHeader(400)
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error(), Data:make([]int, 0)}))
            return
        }

        var netstat NetstatData

        if err := json.Unmarshal(body, &netstat); err != nil {
            log.Printf("[error] %v - %s", err, r.URL.Path)
            w.WriteHeader(400)
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error(), Data:make([]int, 0)}))
            return
        }

        rhost := readUserIP(r)
        records := []config.SockTable{}

        for _, nr := range netstat.Data {
            if nr.LocalAddr.Name == "" {
                log.Printf("[error] parameter missing localAddr.name, sender - %s", rhost)
                continue
            }
            if nr.LocalAddr.IP == nil {
                log.Printf("[error] parameter missing LocalAddr.IP, sender - %s", rhost)
                continue
            }
            if nr.RemoteAddr.Name == "" {
                log.Printf("[error] parameter missing RemoteAddr.Name, sender - %s", rhost)
                continue
            }
            if nr.RemoteAddr.IP == nil {
                log.Printf("[error] parameter missing RemoteAddr.IP, sender - %s", rhost)
                continue
            }
            if nr.Relation.Port == 0 {
                log.Printf("[error] parameter missing Relation.Port, sender - %s", rhost)
                continue
            }
            if nr.Relation.Mode == "" {
                log.Printf("[error] parameter missing Relation.Mode, sender - %s", rhost)
                continue
            }
            if nr.Type == "" {
                nr.Type = "out"
            }
            nr.Id = getIdRec(&nr)
            records = append(records, nr)
        }

        if err := db.DbClient.SaveRecords(*api.db, records); err != nil {
            log.Printf("[error] %v - %s", err, r.URL.Path)
            w.WriteHeader(400)
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error(), Data:make([]int, 0)}))
            return
        }
        
        w.WriteHeader(204)
        return
    }

    if r.Method == "DELETE" {
        body, err := readBody(r)
        if err != nil {
            log.Printf("[error] %v - %s", err, r.URL.Path)
            w.WriteHeader(400)
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error(), Data:make([]int, 0)}))
            return
        }

        var keys []string

        if err := json.Unmarshal(body, &keys); err != nil {
            log.Printf("[error] %v - %s", err, r.URL.Path)
            w.WriteHeader(400)
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error(), Data:make([]int, 0)}))
            return
        }

        if err := db.DbClient.DelRecords(*api.db, keys); err != nil {
            log.Printf("[error] %v - %s", err, r.URL.Path)
            w.WriteHeader(400)
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error(), Data:make([]int, 0)}))
            return
        }

        w.WriteHeader(200)
        w.Write(encodeResp(&Resp{Status:"success", Data:make([]int, 0)}))
        return
    }

    w.WriteHeader(405)
    w.Write(encodeResp(&Resp{Status:"error", Error:"method not allowed", Data:make([]int, 0)}))
}

func (api *Api) ApiExceptions(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")

    if r.Method == "GET" {
        args := config.ExpArgs{}

        for k, v := range r.URL.Query() {
            switch k {
                case "id":
                    args.Id = v[0]
                case "src_name":
                    args.SrcName = v[0]
                case "account_id":
                    i, err := strconv.Atoi(v[0])
                    if err != nil {
                        w.WriteHeader(400)
                        w.Write(encodeResp(&Resp{Status:"error", Error:fmt.Sprintf("executing query: invalid parameter: %v", k), Data:make([]int, 0)}))
                        return
                    }
                    args.AccountID = uint32(i)
            }
        }

        exceptions, err := db.DbClient.LoadExceptions(*api.db, args)
        if err != nil {
            log.Printf("[error] %v - %s", err, r.URL.Path)
            w.WriteHeader(400)
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error(), Data:make([]int, 0)}))
            return
        }

        data := encodeResp(&Resp{Status:"success", Data:exceptions})
        buf, ok, err := compressData(data, r.Header.Get("Accept-Encoding"))
        if err != nil {
            log.Printf("[error] %v - %s", err, r.URL.Path)
            w.WriteHeader(500)
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error(), Data:make([]int, 0)}))
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
        body, err := readBody(r)
        if err != nil {
            log.Printf("[error] %v - %s", err, r.URL.Path)
            w.WriteHeader(400)
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error(), Data:make([]int, 0)}))
            return
        }

        var expdata ExceptionData

        if err := json.Unmarshal(body, &expdata); err != nil {
            log.Printf("[error] %v - %s", err, r.URL.Path)
            w.WriteHeader(400)
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error(), Data:make([]int, 0)}))
            return
        }

        exceptions := []config.Exception{}

        for _, ex := range expdata.Data {
            if ex.Id == "" {
                ex.Id = getIdExp(&ex)
            } 
            exceptions = append(exceptions, ex)
        }

        if err := db.DbClient.SaveExceptions(*api.db, exceptions); err != nil {
            log.Printf("[error] %v - %s", err, r.URL.Path)
            w.WriteHeader(400)
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error(), Data:make([]int, 0)}))
            return
        }
        
        w.WriteHeader(204)
        return
    }

    if r.Method == "DELETE" {
        body, err := readBody(r)
        if err != nil {
            log.Printf("[error] %v - %s", err, r.URL.Path)
            w.WriteHeader(400)
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error(), Data:make([]int, 0)}))
            return
        }

        var keys []string

        if err := json.Unmarshal(body, &keys); err != nil {
            log.Printf("[error] %v - %s", err, r.URL.Path)
            w.WriteHeader(400)
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error(), Data:make([]int, 0)}))
            return
        }

        if err := db.DbClient.DelExceptions(*api.db, keys); err != nil {
            log.Printf("[error] %v - %s", err, r.URL.Path)
            w.WriteHeader(400)
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error(), Data:make([]int, 0)}))
            return
        }

        w.WriteHeader(200)
        w.Write(encodeResp(&Resp{Status:"success", Data:make([]int, 0)}))
        return
    }

    w.WriteHeader(405)
    w.Write(encodeResp(&Resp{Status:"error", Error:"method not allowed", Data:make([]int, 0)}))
}

func (api *Api) ApiWebhook(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")

    if r.Method == "POST" {

        body, err := readBody(r)
        if err != nil {
            log.Printf("[error] %v - %s", err, r.URL.Path)
            w.WriteHeader(400)
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error(), Data:make([]int, 0)}))
            return
        }

        if len(api.conf.Notifier.URLs) > 0 {
            for _, url := range api.conf.Notifier.URLs {
                config := client.HttpConfig{
                    URLs: []string{url},
                }
                go httpClient.WriteRecords(config, "/api/v1/alerts", body)
            }
        }
        
        w.WriteHeader(204)
        return
    }

    w.WriteHeader(405)
    w.Write(encodeResp(&Resp{Status:"error", Error:"method not allowed", Data:make([]int, 0)}))
}