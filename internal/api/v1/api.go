package v1

import (
    "log"
    "net"
    "fmt"
    "net/http"
    "time"
    "reflect"
    "strconv"
    "strings"
    "io/ioutil"
    "encoding/json"
    "github.com/ltkh/netmap/internal/config"
    "github.com/neo4j/neo4j-go-driver/v4/neo4j"
)

type NetstatData struct {
    Group          string                 `json:"group"`
    Data           []SockTable            `json:"data"`
}

// SockTable type represents each line of the /proc/net/[tcp|udp]
type SockTable struct {
    Relation       map[string]interface{} `json:"relation"`
    LocalAddr      *SockAddr              `json:"localAddr"`
    RemoteAddr     *SockAddr              `json:"remoteAddr"`
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

type ApiTraceroute struct {
    Databases      []*config.Database
}

func runTransaction(driver neo4j.Driver, transact Transaction) (interface{}, error) {
    session := driver.NewSession(neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
    defer session.Close()

    result, err := session.WriteTransaction(func(transaction neo4j.Transaction) (interface{}, error) {
        result, err := transaction.Run(transact.Cypher, transact.Params)
        if err != nil {
            return nil, err
        }
        var arr []interface{}
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

func (a *ApiStatus) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(204)
}

func (a *ApiTraceroute) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(204)
}

func (a *ApiRecords) ServeHTTP(w http.ResponseWriter, r *http.Request) {

    var condit []string 
    params := map[string]interface{}{}

    for k, v := range r.URL.Query() {
        switch k {
            case "rel_result":
                result, err := strconv.Atoi(v[0])
                if err != nil {
                    log.Printf("[error] %v", err)
                    w.WriteHeader(500)
                }
                params[k] = result
                condit = append(condit, "r.result = $rel_result")
            case "rel_port":
                port, err := strconv.Atoi(v[0])
                if err != nil {
                    log.Printf("[error] %v", err)
                    w.WriteHeader(500)
                }
                params[k] = port
                condit = append(condit, "r.port = $rel_port")
            case "rel_mode":
                params[k] = v[0]
                condit = append(condit, "r.mode = $rel_mode")
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

        cypher := "MATCH (a)-[r:relation]->(b) RETURN a,r,b";
        if len(condit) > 0 {
            cypher = "MATCH (a)-[r:relation]->(b) WHERE "+strings.Join(condit, " AND ")+" RETURN a,r,b"
        }
        arr, err := runTransaction(driver, Transaction{
            Cypher: cypher+" LIMIT 1000",
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

            driver, err := neo4j.NewDriver(db.Uri, neo4j.BasicAuth(db.UserName, db.Password, ""))
            if err != nil {
                log.Printf("[error] %v", err)
                return
            }
            defer driver.Close()

            for _, v := range t.Data {

                _, err := runTransaction(driver, Transaction{
                    Cypher: fmt.Sprintf("MERGE(host:%s { name: $name, ip: $ip })", t.Group),
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
                    Cypher: fmt.Sprintf("MERGE(host:%s { name: $name, ip: $ip })", t.Group),
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
                    Cypher: fmt.Sprintf("MATCH (a:%s { ip: $src_ip }),(b:%s { ip: $dst_ip }) MERGE (a)-[r:relation{ mode: 'tcp', port: $port, result: 0, trace: 0, status: 'added' }]->(b)", t.Group, t.Group), 
                    Params: map[string]interface{}{
                        "src_ip": v.LocalAddr.IP.String(),
                        "dst_ip": v.RemoteAddr.IP.String(),
                        "port": v.RemoteAddr.Port,
                    },
                })
                if err != nil {
                    log.Printf("[error] %v", err)
                    continue
                }

                _, err = runTransaction(driver, Transaction{
                    Cypher: "MATCH (a)-[r:relation]->(b) WHERE a.ip = $src_ip AND b.ip = $dst_ip AND r.port = $port SET r.update = $update", 
                    Params: map[string]interface{}{
                        "src_ip": v.LocalAddr.IP.String(),
                        "dst_ip": v.RemoteAddr.IP.String(),
                        "port": v.RemoteAddr.Port,
                        "update": time.Now().UTC().Unix(),
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