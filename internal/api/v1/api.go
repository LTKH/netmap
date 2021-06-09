package v1

import (
    //"log"
    "net"
    //"fmt"
    //"net/http"
    //"time"
    "reflect"
    //"strconv"
    //"strings"
    //"io/ioutil"
    //"encoding/json"
    //"github.com/ltkh/netmap/internal/client"
    "github.com/ltkh/netmap/internal/config"
    "github.com/neo4j/neo4j-go-driver/v4/neo4j"
    "github.com/prometheus/client_golang/prometheus"
)

var (
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

type NetstatData struct {
    Data           []SockTable            `json:"data"`
}

// SockTable type represents each line of the /proc/net/[tcp|udp]
type SockTable struct {
    Relation       *Relation              `json:"relation"`
    LocalAddr      *SockAddr              `json:"localAddr"`
    RemoteAddr     *SockAddr              `json:"remoteAddr"`
}

type Relation struct {
    Mode           string                 `json:"mode"`
    Port           uint16                 `json:"port"`
    Result         int                    `json:"result"`
    Response       float64                `json:"response"`
    Trace          int                    `json:"trace"`
    Status         string                 `json:"status"`
}

// SockAddr represents an ip:port pair
type SockAddr struct {
    IP             net.IP                 `json:"ip"`
    Port           uint16                 `json:"port"`
    Name           string                 `json:"name"`
}

type Transaction struct {
    Cypher         string 
    Params         map[string]interface{}
}

type Response struct {
    Status         string                 `json:"status"`
    Error          string                 `json:"error,omitempty"`
    Warnings       []string               `json:"warnings,omitempty"`
    Data           interface{}            `json:"data"`
}

type ApiRecords struct {
    Databases      []*config.Database
}

type ApiNetstat struct {
    Databases      []*config.Database
}

type ApiStatus struct {
    Databases      []*config.Database
}

type ApiWebhook struct {
    Alerting       *config.Alerting
}

type Alert struct {
    Status         string                 `json:"status,omitempty"`
    Labels         map[string]string      `json:"labels"`
    Annotations    Annotations            `json:"annotations"`
}

type Annotations struct {
    Description    string                 `json:"description"`
}

func MonRegister(){
    prometheus.MustRegister(resultCode)
    prometheus.MustRegister(responseTime)
}

func runTransaction(driver neo4j.Driver, transact Transaction) (interface{}, error) {
    session := driver.NewSession(neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
    defer session.Close()

    result, err := session.WriteTransaction(func(transaction neo4j.Transaction) (interface{}, error) {
        var arr []interface{}

        result, err := transaction.Run(transact.Cypher, transact.Params)
        if err != nil {
            return nil, err
        }

        keys := [3]string{"localAddr","relation","remoteAddr"}
        for result.Next() {
            a := map[string]interface{}{}
            for k, _ := range result.Record().Values {
                r := reflect.ValueOf(result.Record().Values[k])
                f := reflect.Indirect(r).FieldByName("Props")
                a[keys[k]] = f.Interface()
            }
            arr = append(arr, a)
		}

        return arr, nil
    })
    if err != nil {
        return nil, err
    }

    return result, nil
}

/*
func (a *ApiWebhook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    body, err := ioutil.ReadAll(r.Body)
    if err != nil {
        log.Printf("[error] %v - %s", err, r.URL.Path)
        w.WriteHeader(400)
        w.Write([]byte(err.Error()))
        return
    }

    for _, m := range a.Alerting.Alertmanagers {
        for _, s := range m.StaticConfigs {
            for _, t := range s.Targets {
                go func() {
                    //new client
                    conn := client.New(client.HTTP{
                        URLs:        []string{"http://"+t},
                    })
                    _, err = conn.HttpRequest("POST", "/api/v1/alerts", body)
                    if err != nil {
                        log.Printf("[error] %v", err)
                    }
                }()
            }
        }
    }
    
    w.WriteHeader(204)
}

func (a *ApiRecords) ServeHTTP(w http.ResponseWriter, r *http.Request) {

    var condit []string 
    params := map[string]interface{}{}

    for k, v := range r.URL.Query() {
        switch k {
            case "result":
                result, err := strconv.Atoi(v[0])
                if err != nil {
                    log.Printf("[error] %v", err)
                    w.WriteHeader(500)
                }
                params[k] = result
                condit = append(condit, "r.result = $result")
            case "port":
                port, err := strconv.Atoi(v[0])
                if err != nil {
                    log.Printf("[error] %v", err)
                    w.WriteHeader(500)
                }
                params[k] = port
                condit = append(condit, "r.port = $port")
            case "mode":
                params[k] = v[0]
                condit = append(condit, "r.mode = $mode")
            case "src_name":
                params[k] = v[0]
                condit = append(condit, "a.name =~ $src_name")
            case "src_ip":
                params[k] = v[0]
                condit = append(condit, "a.ip =~ $src_ip")
            case "dst_name":
                params[k] = v[0]
                condit = append(condit, "b.name =~ $dst_name")
            case "dst_ip":
                params[k] = v[0]
                condit = append(condit, "b.ip =~ $dst_ip")
        }
    }

    for _, db := range a.Databases {

        driver, err := neo4j.NewDriver(db.Uri, neo4j.BasicAuth(db.UserName, db.Password, ""))
        if err != nil {
            log.Printf("[error] %v", err)
            w.WriteHeader(500)
            return
        }
        defer driver.Close()

        cypher := "MATCH (a)-[r:relation]->(b) RETURN a,r,b LIMIT 1000";
        if len(condit) > 0 {
            cypher = "MATCH (a)-[r:relation]->(b) WHERE "+strings.Join(condit, " AND ")+" RETURN a,r,b LIMIT 1000"
        }
        arr, err := runTransaction(driver, Transaction{
            Cypher: cypher,
            Params: params,
        })
        if err != nil {
            log.Printf("[error] %v", err)
            w.WriteHeader(500)
            return
        }

        var resp Response
        resp.Status = "success"
        resp.Data = arr
        jsn, err := json.Marshal(resp)
        if err != nil {
            log.Printf("[error] %v", err)
            w.WriteHeader(500)
            return
        }

        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(200)
        w.Write([]byte(jsn))
        return

    }

    w.WriteHeader(500)
}

func (a *ApiNetstat) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    var t NetstatData

    body, err := ioutil.ReadAll(r.Body)
    if err != nil {
        log.Printf("[error] %v - %s", err, r.URL.Path)
        w.WriteHeader(400)
        w.Write([]byte(err.Error()))
        return
    }

    if err := json.Unmarshal(body, &t); err != nil {
        log.Printf("[error] %v - %s", err, r.URL.Path)
        w.WriteHeader(400)
        w.Write([]byte(err.Error()))
        return
    }

    for _, db := range a.Databases {

        go func(t NetstatData, db *config.Database){

            group := "test"

            driver, err := neo4j.NewDriver(db.Uri, neo4j.BasicAuth(db.UserName, db.Password, ""))
            if err != nil {
                log.Printf("[error] %v", err)
                return
            }
            defer driver.Close()

            for _, v := range t.Data {

                _, err := runTransaction(driver, Transaction{
                    Cypher: fmt.Sprintf("MERGE (host:%s { name: $name, ip: $ip })", group),
                    Params: map[string]interface{}{
                        "name": v.LocalAddr.Name,
                        "ip": v.LocalAddr.IP.String(),
                    },
                })
                if err != nil {
                    log.Printf("[error] %v", err)
                    continue
                }

                _, err = runTransaction(driver, Transaction{
                    Cypher: fmt.Sprintf("MERGE (host:%s { name: $name, ip: $ip })", group),
                    Params: map[string]interface{}{
                        "name": v.RemoteAddr.Name,
                        "ip": v.RemoteAddr.IP.String(),
                    },
                })
                if err != nil {
                    log.Printf("[error] %v", err)
                    continue
                }

                _, err = runTransaction(driver, Transaction{
                    Cypher: fmt.Sprintf("MATCH (a:%s { name: $src_name }),(b { name: $dst_name }) MERGE (a)-[r:relation { mode: $mode, port: $port }]->(b)", group),
                    Params: map[string]interface{}{
                        "src_name":   v.LocalAddr.Name,
                        "dst_name":   v.RemoteAddr.Name,
                        "mode":       v.Relation.Mode,
                        "port":       v.Relation.Port,
                    },
                })
                if err != nil {
                    log.Printf("[error] %v", err)
                    continue
                }

                _, err = runTransaction(driver, Transaction{
                    Cypher: fmt.Sprintf("MATCH (a:%s { name: $src_name })-[r:relation { port: $port, mode: $mode }]->(b { name: $dst_name }) SET r.result = 0, r.response = 0.0, r.trace = 0, r.update = $update", group),
                    Params: map[string]interface{}{
                        "src_name":   v.LocalAddr.Name,
                        "dst_name":   v.RemoteAddr.Name,
                        "mode":       v.Relation.Mode,
                        "port":       v.Relation.Port,
                        "update":     time.Now().UTC().Unix(),
                    },
                })
                if err != nil {
                    log.Printf("[error] %v", err)
                    continue
                }

            }
        }(t, db)
    }

    w.WriteHeader(204)
}

func (a *ApiStatus) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    var t NetstatData

    body, err := ioutil.ReadAll(r.Body)
    if err != nil {
        log.Printf("[error] %v - %s", err, r.URL.Path)
        w.WriteHeader(400)
        w.Write([]byte(err.Error()))
        return
    }

    if err := json.Unmarshal(body, &t); err != nil {
        log.Printf("[error] %v - %s", err, r.URL.Path)
        w.WriteHeader(400)
        w.Write([]byte(err.Error()))
        return
    }

    for _, db := range a.Databases {

        go func(t NetstatData, db *config.Database){

            group := "test"

            driver, err := neo4j.NewDriver(db.Uri, neo4j.BasicAuth(db.UserName, db.Password, ""))
            if err != nil {
                log.Printf("[error] %v", err)
                return
            }
            defer driver.Close()

            for _, v := range t.Data {

                //write status to DB
                _, err = runTransaction(driver, Transaction{
                    Cypher: fmt.Sprintf("MATCH (a:%s { name: $src_name })-[r:relation { port: $port, mode: $mode }]->(b { name: $dst_name }) SET r.result = $result, r.response = $response, r.trace = $trace", group),
                    Params: map[string]interface{}{
                        "src_name":   v.LocalAddr.Name,
                        "dst_name":   v.RemoteAddr.Name,
                        "mode":       v.Relation.Mode,
                        "port":       v.Relation.Port,
                        "trace":      v.Relation.Trace,
                        "result":     v.Relation.Result,
                        "response":   v.Relation.Response,
                    },
                })
                if err != nil {
                    log.Printf("[error] %v", err)
                    continue
                }

                resultCode.With(prometheus.Labels{ 
                    "src_name": v.LocalAddr.Name, 
                    "dst_name": v.RemoteAddr.Name, 
                    "port":     fmt.Sprintf("%v", v.Relation.Port), 
                    "mode":     fmt.Sprintf("%v", v.Relation.Mode), 
                }).Set(float64(v.Relation.Result))

                responseTime.With(prometheus.Labels{ 
                    "src_name": v.LocalAddr.Name, 
                    "dst_name": v.RemoteAddr.Name, 
                    "port":     fmt.Sprintf("%v", v.Relation.Port), 
                    "mode":     fmt.Sprintf("%v", v.Relation.Mode), 
                }).Set(v.Relation.Response)
            }

        }(t, db)
    }

    w.WriteHeader(204)
}
*/