package v1

import (
    "log"
    "fmt"
    "net/http"
    "time"
    "errors"
    "compress/gzip"
    "io"
    "bytes"
    "regexp"
    "io/ioutil"
    "encoding/json"
    "github.com/prometheus/client_golang/prometheus"
    "github.com/ltkh/netmap/internal/config"
    "github.com/ltkh/netmap/internal/cache"
    "github.com/ltkh/netmap/internal/client"
)

var (
    httpClient = client.NewHttpClient()
    clusterID = getClusterID()

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
    Conf         *config.Config
    CacheRecords *cache.Records
}

type Resp struct {
    Status       string                    `json:"status"`
    Error        string                    `json:"error,omitempty"`
    Warnings     []string                  `json:"warnings,omitempty"`
    Data         interface{}               `json:"data"`
}

type NetstatData struct {
    Data         []cache.SockTable         `json:"data"`
}

func getClusterID() string {
    return cache.GetHash(fmt.Sprintf("%v", time.Now().UnixNano()))
}

func encodeResp(resp *Resp) []byte {
    jsn, err := json.Marshal(resp)
    if err != nil {
        return encodeResp(&Resp{Status:"error", Error:err.Error(), Data:make([]int, 0)})
    }
    return jsn
}

func MonRegister(){
    prometheus.MustRegister(resultCode)
    prometheus.MustRegister(responseTime)
}

func New(cfg *config.Config) (*Api, error) {

    var api Api

    // Set CacheLimit
    if cfg.Cache.Limit == 0 {
        cfg.Cache.Limit = 1000000
    }

    // Set CacheFlushInterval
    if cfg.Cache.FlushInterval == "" {
        cfg.Cache.FlushInterval = "24h"
    }
    flushInterval, _ := time.ParseDuration(cfg.Cache.FlushInterval)
    if flushInterval == 0 {
        return &api, errors.New("setting cache flush interval: invalid duration")
    }

    api.Conf = cfg
    api.CacheRecords = cache.NewCacheRecords(cfg.Cache.Limit, flushInterval)

    return &api, nil
}

func (api *Api) ApiRecords(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")

    if r.Header.Get("Cluster-ID") == clusterID {
        w.WriteHeader(200)
        w.Write(encodeResp(&Resp{Status:"success", Data:make([]int, 0)}))
        return
    }

    if r.Method == "GET" && r.URL.Path == "/api/v1/netmap/records" {

        var records []cache.SockTable

        strArgs := make(map[string]string)

        for k, v := range r.URL.Query() {
            switch k {
                case "src_name":
                    strArgs[k] = v[0]
                default:
                    w.WriteHeader(400)
                    w.Write(encodeResp(&Resp{Status:"error", Error:fmt.Sprintf("executing query: invalid parameter: %v", k), Data:make([]int, 0)}))
                    return
            }
        }

        for key, item := range api.CacheRecords.Items() {
            if strArgs["src_name"] != "" && strArgs["src_name"] != item.LocalAddr.Name {
                continue
            }
            item.Id = key
            records = append(records, item)
        }

        if len(records) == 0 {
            records = make([]cache.SockTable, 0)
        }

        var buf bytes.Buffer

        data := encodeResp(&Resp{Status:"success", Data:records})

        // Send compressed data if needed
        matched, _ := regexp.MatchString(`gzip`, r.Header.Get("Accept-Encoding"))
        if matched {
            writer := gzip.NewWriter(&buf)
            if _, err := writer.Write(data); err != nil {
                log.Printf("[error] %v - %s", err, r.URL.Path)
                w.WriteHeader(500)
                w.Write(encodeResp(&Resp{Status:"error", Error:"unable to compress data", Data:make([]int, 0)}))
                return
            }
            if err := writer.Close(); err != nil {
                log.Printf("[error] %v - %s", err, r.URL.Path)
                w.WriteHeader(500)
                w.Write(encodeResp(&Resp{Status:"error", Error:"unable to compress data", Data:make([]int, 0)}))
                return
            }
            w.Header().Set("Content-Encoding", "gzip")
        } else {
            buf = *bytes.NewBuffer(data)
        }

        w.WriteHeader(200)
        w.Write(buf.Bytes())

        return
    }

    if r.Method == "DELETE" && r.URL.Path == "/api/v1/netmap/records" {

        var reader io.ReadCloser
        var err error
        var keys []string

        // Check that the server actual sent compressed data
        switch r.Header.Get("Content-Encoding") {
            case "gzip":
                reader, err = gzip.NewReader(r.Body)
                if err != nil {
                    log.Printf("[error] %v - %s", err, r.URL.Path)
                    w.WriteHeader(400)
                    w.Write(encodeResp(&Resp{Status:"error", Error:err.Error(), Data:make([]int, 0)}))
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
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error(), Data:make([]int, 0)}))
            return
        }

        if err := json.Unmarshal(body, &keys); err != nil {
            log.Printf("[error] %v - %s", err, r.URL.Path)
            w.WriteHeader(400)
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error(), Data:make([]int, 0)}))
            return
        }

        for _, k := range keys {
            api.CacheRecords.Del(k)
        }

        if len(api.Conf.Cluster.URLs) > 0 && r.Header.Get("Cluster-ID") == "" {

            for _, url := range api.Conf.Cluster.URLs {
                config := client.HttpConfig{
                    URLs: []string{url},
                    Headers: map[string]string{
                        "Cluster-ID": clusterID,
                    },
                }
                go httpClient.DelRecords(config, r.URL.Path, body)
            }

        }

        w.WriteHeader(200)
        w.Write(encodeResp(&Resp{Status:"success", Data:make([]int, 0)}))

        return
    }

    if r.Method == "POST" {

        var netstat NetstatData
        var reader io.ReadCloser
        var err error

        // Check that the server actual sent compressed data
        switch r.Header.Get("Content-Encoding") {
            case "gzip":
                reader, err = gzip.NewReader(r.Body)
                if err != nil {
                    log.Printf("[error] %v - %s", err, r.URL.Path)
                    w.WriteHeader(400)
                    w.Write(encodeResp(&Resp{Status:"error", Error:err.Error(), Data:make([]int, 0)}))
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
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error(), Data:make([]int, 0)}))
            return
        }

        if err := json.Unmarshal(body, &netstat); err != nil {
            log.Printf("[error] %v - %s", err, r.URL.Path)
            w.WriteHeader(400)
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error(), Data:make([]int, 0)}))
            return
        }

        switch r.URL.Path {
            case "/api/v1/netmap/netstat":
                for _, nr := range netstat.Data {
                    id := cache.GetID(&nr)
                    val, ok := api.CacheRecords.Get(id)
                    if ok {
                        if err := api.CacheRecords.Set(id, val, true); err != nil {
                            log.Printf("[error] %v - %s", err, r.URL.Path)
                        }
                    } else {
                        if err := api.CacheRecords.Set(id, nr, true); err != nil {
                            log.Printf("[error] %v - %s", err, r.URL.Path)
                        }
                    }
                }
            case "/api/v1/netmap/records":
                for _, nr := range netstat.Data {
                    id := cache.GetID(&nr)
                    if err := api.CacheRecords.Set(id, nr, true); err != nil {
                        log.Printf("[error] %v - %s", err, r.URL.Path)
                    }
                }
            case "/api/v1/netmap/status":
                for _, nr := range netstat.Data {
                    id := cache.GetID(&nr)
                    if err := api.CacheRecords.Set(id, nr, false); err != nil {
                        log.Printf("[error] %v - %s", err, r.URL.Path)
                    }
                }
        }

        if len(api.Conf.Cluster.URLs) > 0 && r.Header.Get("Cluster-ID") == "" {

            for _, url := range api.Conf.Cluster.URLs {
                config := client.HttpConfig{
                    URLs: []string{url},
                    Headers: map[string]string{
                        "Cluster-ID": clusterID,
                    },
                }
                go httpClient.WriteRecords(config, r.URL.Path, body)
            }

        }
        
        w.WriteHeader(204)
        return
    }

    w.WriteHeader(405)
    w.Write(encodeResp(&Resp{Status:"error", Error:"method not allowed", Data:make([]int, 0)}))
}

func (api *Api) ApiWebhook(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")

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
                    w.Write(encodeResp(&Resp{Status:"error", Error:err.Error(), Data:make([]int, 0)}))
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
            w.Write(encodeResp(&Resp{Status:"error", Error:err.Error(), Data:make([]int, 0)}))
            return
        }

        if len(api.Conf.Notifier.URLs) > 0 {
            for _, url := range api.Conf.Notifier.URLs {
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

func (api *Api) ApiDelExpiredItems() {
    api.CacheRecords.DelExpiredItems()
}

func (api *Api) ApiGetClusterRecords() {
    if len(api.Conf.Cluster.URLs) > 0 {
        for _, url := range api.Conf.Cluster.URLs {
            items := api.CacheRecords.Items()

            config := client.HttpConfig{
                URLs: []string{url},
                Headers: map[string]string{
                    "Cluster-ID": clusterID,
                },
            }

            var nrs NetstatData

            body, err := httpClient.ReadRecords(config, "/api/v1/netmap/records")
            if err != nil {
                continue
            }

            if err := json.Unmarshal(body, &nrs); err != nil {
                log.Printf("[error] %v - %s", err, "/api/v1/netmap/records")
                continue
            } 

            for _, nr := range nrs.Data {
                id := cache.GetID(&nr)
                val, ok := items[id]
                if ok && val.Options.ActiveTime >= nr.Options.ActiveTime {
                    continue
                }
                if err := api.CacheRecords.Set(id, nr, true); err != nil {
                    log.Printf("[error] %v - %s", err, "/api/v1/netmap/records")
                }
            }
        }
    }
}