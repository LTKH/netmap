package netstat

import (
    "os"
    "net"
    "time"
    //"log"
    //"encoding/json"
    "fmt"
    "strings"
    "github.com/ltkh/netmap/internal/cache"
    "github.com/ltkh/netmap/internal/api/v1"
    ns "github.com/cakturk/go-netstat/netstat"
)

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

func ignorePorts(port uint16, iports []uint16) bool {
    for _, p := range iports {
        if port == p {
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

func GetSocks(iports []uint16, options cache.Options) (v1.NetstatData, error) {
    var nd v1.NetstatData
    
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

            //jsn, _ := json.Marshal(e)
            //log.Printf("[debug] %v", string(jsn))

            if e.State == ns.Listen {
                continue
            }
            if e.LocalAddr.IP.String() == e.RemoteAddr.IP.String() {
                continue
            }
            if ignorePorts(e.RemoteAddr.Port, iports) {
                continue
            }

            id := fmt.Sprintf("%v:%v", e.RemoteAddr.IP, e.RemoteAddr.Port)
            if _, ok := ks[id]; ok {
                continue
            }
            ks[id] = id
            
            addr, err := lookupAddr(e.RemoteAddr.IP.String())
            if err != nil {
                continue
            }

            conn, err := net.DialTimeout(mode, e.RemoteAddr.String(), time.Duration(options.Timeout) * time.Second)
            if err != nil {
                continue
            }
            defer conn.Close()

            nd.Data = append(nd.Data, cache.SockTable{
                LocalAddr: cache.SockAddr{
                    IP:    e.LocalAddr.IP,
                    Name:  name,
                },
                RemoteAddr: cache.SockAddr{
                    IP:    e.RemoteAddr.IP,
                    Name:  addr,
                },
                Relation: cache.Relation{
                    Mode:  mode,
                    Port:  e.RemoteAddr.Port,
                },
                Options: options,
            })
        }
    }

    return nd, nil
}