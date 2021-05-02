package netstat

import (
    "net"
    "time"
    ns "github.com/cakturk/go-netstat/netstat"
    "github.com/ltkh/netmap/internal/api/v1"
)

func ignorePorts(port uint16, iports []uint16) bool {
    for _, p := range iports {
        if port == p {
            return true
        }
    }
    return false
}

func getHostName(ipAddress string) string {
    name, err := net.LookupAddr(ipAddress)
    if err == nil {
        return name[0]
    }
    return "unknown"
}

func GetSocks(iports []uint16) (v1.NetstatData, error) {
    var nd v1.NetstatData

    // TCP sockets
    socks, err := ns.TCPSocks(ns.NoopFilter)
    if err != nil {
        return nd, err
    }

    ks := make(map[string]string)

    for _, e := range socks {

        if e.State == ns.Listen {
            continue
        }
        if e.LocalAddr.IP.String() == e.RemoteAddr.IP.String() {
            continue
        }
        if ignorePorts(e.RemoteAddr.Port, iports) {
            continue
        }
        if _, ok := ks[e.RemoteAddr.String()]; ok {
            continue
        }

        ks[e.RemoteAddr.String()] = e.RemoteAddr.String()

        conn, err := net.DialTimeout("tcp", e.RemoteAddr.String(), 1 * time.Second)
        if err == nil {
            //localName, err := net.LookupAddr(e.LocalAddr.IP.String())
            nd.Data = append(nd.Data, v1.SockTable{
                LocalAddr: &v1.SockAddr{
                    IP:    e.LocalAddr.IP,
                    Port:  e.LocalAddr.Port,
                    Name:  getHostName(e.LocalAddr.IP.String()),
                },
                RemoteAddr: &v1.SockAddr{
                    IP:    e.RemoteAddr.IP,
                    Port:  e.RemoteAddr.Port,
                    Name:  getHostName(e.RemoteAddr.IP.String()),
                },
            })
            defer conn.Close()
        }
    }

    return nd, nil
}