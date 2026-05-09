package netstat

import (
	"fmt"
	"log"
	"net"
	"os"
	"regexp"
	"strings"

	"github.com/ltkh/netmap/internal/config"
)

type NetstatData struct {
	Data []config.SockTable `json:"data"`
}

type SockAddr struct {
	IP   net.IP
	Port uint16
}

type SockTabEntry struct {
	ino        string
	LocalAddr  *SockAddr
	RemoteAddr *SockAddr
	State      SkState
	UID        uint32
	Process    *Process
}

type Process struct {
	Pid  int
	Name string
}

func Hostname() (string, error) {
	if os.Getenv("NETAGENT_HOSTNAME") != "" {
		return os.Getenv("NETAGENT_HOSTNAME"), nil
	}
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

func (s *SockAddr) String() string {
	return fmt.Sprintf("%v:%d", s.IP, s.Port)
}

func (p *Process) String() string {
	return fmt.Sprintf("%d/%s", p.Pid, p.Name)
}

type SkState uint8

func (s SkState) String() string {
	return skStates[s]
}

type AcceptFn func(*SockTabEntry) bool

func NoopFilter(*SockTabEntry) bool { return true }

func TCPSocks(accept AcceptFn) ([]SockTabEntry, error) {
	return osTCPSocks(accept)
}

func TCP6Socks(accept AcceptFn) ([]SockTabEntry, error) {
	return osTCP6Socks(accept)
}

func UDPSocks(accept AcceptFn) ([]SockTabEntry, error) {
	return osUDPSocks(accept)
}

func UDP6Socks(accept AcceptFn) ([]SockTabEntry, error) {
	return osUDP6Socks(accept)
}

func GetSocks(ihosts []string, ids map[string]bool, options config.Options, incoming, debug bool) (NetstatData, error) {
	var nd NetstatData
	var err error

	hostname, e := Hostname()
	if e != nil {
		return nd, e
	}

	// Get socks
	for _, mode := range []string{"tcp", "udp"} {

		var socks []SockTabEntry

		switch mode {
		case "tcp":
			socks, err = TCPSocks(NoopFilter)
			if err != nil {
				return nd, err
			}
		case "udp":
			socks, err = UDPSocks(NoopFilter)
			if err != nil {
				return nd, err
			}
		}

		ports := map[uint16]net.IP{}
		for _, e := range socks {
			if e.RemoteAddr.IP.String() == "0.0.0.0" && e.RemoteAddr.Port == 0 {
				ports[e.LocalAddr.Port] = e.LocalAddr.IP
			}
		}

		for _, e := range socks {
			if e.RemoteAddr.IP.String() == "0.0.0.0" {
				continue
			}

			if e.RemoteAddr.IP.String() == "127.0.0.1" {
				continue
			}

			if e.LocalAddr.IP.String() == e.RemoteAddr.IP.String() {
				continue
			}

			if e.LocalAddr.Port == 0 || e.RemoteAddr.Port == 0 {
				continue
			}

			rec := config.SockTable{
				LocalAddr: config.SockAddr{
					IP:   e.LocalAddr.IP.String(),
					Port: uint32(e.LocalAddr.Port),
					//Name:        "",
				},
				RemoteAddr: config.SockAddr{
					IP:   e.RemoteAddr.IP.String(),
					Port: uint32(e.RemoteAddr.Port),
					//Name:        "",
				},
				Relation: config.Relation{
					Mode: mode,
					//Port:        e.RemoteAddr.Port,
					//Type:        "",
				},
				Options: config.Options{
					Status:      options.Status,
					Timeout:     options.Timeout,
					MaxRespTime: options.MaxRespTime,
					//Service:     e.Process.Name,
					AccountID: options.AccountID,
				},
			}

			if e.Process != nil {
				rec.Options.Service = e.Process.Name
			}

			if addr, err := lookupAddr(e.LocalAddr.IP.String()); err == nil {
				rec.LocalAddr.Name = addr

				_, ok := ports[e.LocalAddr.Port]
				if addr == hostname && ok {
					rec.Relation.Type = "incoming"
				}
			}

			if addr, err := lookupAddr(e.RemoteAddr.IP.String()); err == nil {
				rec.RemoteAddr.Name = addr
			}

			if ignoreHosts(rec.RemoteAddr.Name, e.RemoteAddr.Port, ihosts) {
				continue
			}

			if debug == true {
				log.Printf("[debug] netstat list %v %v:%v - %v:%v (%v)", mode, rec.LocalAddr.Name, e.LocalAddr.Port, rec.RemoteAddr.Name, e.RemoteAddr.Port, rec.Relation.Type)
			}

			//rec.Id = config.GetIdRec(&rec)
			nd.Data = append(nd.Data, rec)
		}

	}

	return nd, nil
}
