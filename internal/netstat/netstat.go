package netstat

import (
    "os"
    "net"
    "time"
    "log"
    //"encoding/json"
    "regexp"
    "fmt"
    "strings"
    "github.com/ltkh/netmap/internal/config"
    //"github.com/ltkh/netmap/internal/cache"
    ns "github.com/cakturk/go-netstat/netstat"
)

type NetstatData struct {
    Data           []config.SockTable      `json:"data"`
}

func Hostname() (string, error) {
    hostname, err := os.Hostname()
    if err != nil {
        return "", err
    }
    if len(hostname) == 0 {
        return "", fmt.Errorf("empty hostname")
    }
    return hostname, nil
}

func ignoreHosts(host string, port uint16, ihosts []string) bool {
    for _, h := range ihosts {
        hst := fmt.Sprintf("%s:%v", host, port)
        match, err := regexp.MatchString(h, hst)
        if err != nil {
            log.Printf("[error] %v", err)
            continue
        }
        if match {
            return true
        }
    }
    return false
}

func lookupAddr(ipAddress string) (string, error) {
    name, err := net.LookupAddr(ipAddress)
    if err != nil {
        return ipAddress, nil
    }
    if len(name) == 0 {
        return ipAddress, nil
    }
    return strings.Trim(name[0], "."), nil
}

func GetSocks(ihosts []string, options config.Options, debug bool) (NetstatData, error) {
    var nd NetstatData
    
    // Get hostname
    name, err := Hostname()
    if err != nil {
        return nd, err
    }

    // Get socks
    for _, mode := range []string{"tcp", "udp"} {

        var socks []ns.SockTabEntry

        switch mode {
            case "tcp":
                socks, err = ns.TCPSocks(ns.NoopFilter)
                if err != nil {
                    return nd, err
                }
            case "udp":
                socks, err = ns.UDPSocks(ns.NoopFilter)
                if err != nil {
                    return nd, err
                }
        }

        ks := make(map[string]string)

        for _, e := range socks {

            id := fmt.Sprintf("%v:%v:%v", e.RemoteAddr.IP, e.RemoteAddr.Port, mode)
            if _, ok := ks[id]; ok {
                continue
            }
            ks[id] = id

            addr, err := lookupAddr(e.RemoteAddr.IP.String())
            if err != nil {
                continue
            }

            if debug == true {
                log.Printf("[debug] netstat list - %v:%v:%v", addr, e.RemoteAddr.Port, mode)
            }

            if e.LocalAddr.IP.String() == e.RemoteAddr.IP.String() {
                continue
            }

            if e.RemoteAddr.IP.String() == "0.0.0.0" {
                continue
            }

            if e.RemoteAddr.Port == 0 {
                continue
            }

            if ignoreHosts(addr, e.RemoteAddr.Port, ihosts){
                continue
            }

            conn, err := net.DialTimeout(mode, e.RemoteAddr.String(), 3 * time.Second)
            if err != nil {
                continue
            }
            defer conn.Close()

            if e.Process == nil {
                e.Process = &ns.Process{}
            }

            rec := config.SockTable{
                LocalAddr: config.SockAddr{
                    IP:          e.LocalAddr.IP,
                    Name:        name,
                },
                RemoteAddr: config.SockAddr{
                    IP:          e.RemoteAddr.IP,
                    Name:        addr,
                },
                Relation: config.Relation{
                    Mode:        mode,
                    Port:        e.RemoteAddr.Port,
                },
                Options: config.Options {
                    Status:      options.Status,
                    Timeout:     options.Timeout,
                    MaxRespTime: options.MaxRespTime,
                    Service:     e.Process.Name,
                    AccountID:   options.AccountID,
                },
            }

            rec.Id = config.GetIdRec(&rec)
            nd.Data = append(nd.Data, rec)
        }
    }

    return nd, nil
}