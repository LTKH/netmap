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

func GetSocks(ihosts []string, options config.Options, incoming, debug bool) (NetstatData, error) {
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

            if len(nd.Data) > 100 {
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

            if _, ok := ks[config.GetIdRec(&rec)]; ok {
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
                    rec.LocalAddr.Name = addr
                    rec.RemoteAddr.IP = e.LocalAddr.IP
                    rec.RemoteAddr.Name = name
                    rec.Relation.Port = e.LocalAddr.Port

                    if _, ok := ks[config.GetIdRec(&rec)]; ok {
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
            nd.Data = append(nd.Data, rec)
            ks[rec.Id] = rec.Id
        }
    }

    return nd, nil
}