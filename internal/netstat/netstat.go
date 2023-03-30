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

func alreadyExists(ids map[string]bool, rec config.SockTable) (config.SockTable, bool) {

    rec.Id = config.GetIdRec(&rec)
    if _, ok := ids[rec.Id]; ok {
        return rec, true
    }

    localAddr := rec.LocalAddr
    remoteAddr := rec.RemoteAddr

    rec.LocalAddr = remoteAddr
    rec.LocalAddr = localAddr
    rec.Relation.Port = localAddr.Port

    rec.Id = config.GetIdRec(&rec)
    if _, ok := ids[rec.Id]; ok {
        return rec, true
    }

    return rec, false
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

func GetSocks(ihosts []string, ids map[string]bool, options config.Options, incoming, debug bool) (NetstatData, error) {
    var nd NetstatData
    
    nr := map[string]config.SockTable{}
    
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

        for _, e := range socks {

            if len(nr) > 1000 {
                break
            }

            if e.RemoteAddr.IP.String() == "0.0.0.0" {
                continue
            }

            if e.LocalAddr.IP.String() == e.RemoteAddr.IP.String() {
                continue
            }

            if e.RemoteAddr.Port == 0 {
                continue
            }

            addr, err := lookupAddr(e.RemoteAddr.IP.String())
            if err != nil {
                log.Printf("[error] %v", err)
                continue
            }

            if ignoreHosts(addr, e.RemoteAddr.Port, ihosts){
                continue
            }

            if e.Process == nil {
                e.Process = &ns.Process{}
            }

            rec := config.SockTable{
                LocalAddr: config.SockAddr{
                    IP:          e.LocalAddr.IP,
                    Port:        e.LocalAddr.Port,
                    Name:        name,
                },
                RemoteAddr: config.SockAddr{
                    IP:          e.RemoteAddr.IP,
                    Port:        e.RemoteAddr.Port,
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

            if _, ok := nr[config.GetIdRec(&rec)]; ok {
                continue
            }

            // Record already exists in the cache
            if erec, ok := alreadyExists(ids, rec); ok {
                nr[erec.Id] = erec
                continue
            }

            conOut, err := net.DialTimeout(mode, e.RemoteAddr.String(), 3 * time.Second)
            if err != nil {

                if incoming {

                    if ignoreHosts(name, e.LocalAddr.Port, ihosts){
                        continue
                    }

                    conIn, err := net.DialTimeout(mode, e.LocalAddr.String(), 3 * time.Second)
                    if err != nil {
                        continue
                    }
                    conIn.Close()

                    rec.LocalAddr.IP = e.RemoteAddr.IP
                    rec.LocalAddr.Port = e.RemoteAddr.Port
                    rec.LocalAddr.Name = addr
                    rec.RemoteAddr.IP = e.LocalAddr.IP
                    rec.RemoteAddr.Port = e.LocalAddr.Port
                    rec.RemoteAddr.Name = name
                    rec.Relation.Port = e.LocalAddr.Port

                    if _, ok := nr[config.GetIdRec(&rec)]; ok {
                        continue
                    }

                } else {
                    continue
                }

            } else {
                conOut.Close()
            }

            if debug == true {
                log.Printf("[debug] netstat list %v:%v - %v:%v (%v)", e.LocalAddr.IP.String(), e.LocalAddr.Port, e.RemoteAddr.IP.String(), e.RemoteAddr.Port, mode)
            }
            
            rec.Id = config.GetIdRec(&rec)
            nr[rec.Id] = rec
        }
    }

    for _, rec := range nr {
        nd.Data = append(nd.Data, rec)
    }

    return nd, nil
}