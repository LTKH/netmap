package config

import (
    "io"
    "fmt"
    "net"
    //"time"
    "io/ioutil"
    "crypto/sha1"
    "encoding/hex"
    "gopkg.in/yaml.v2"
)

type RecArgs struct {
    Id             string
    SrcName        string
    Timestamp      int64
    Type           string
    AccountID      string
}

type ExpArgs struct {
    Id             string
    SrcName        string
    AccountID      string
}

type Exception struct {
    Id             string                 `json:"id,omitempty"`
    AccountID      uint32                 `json:"accountID"`
    HostMask       string                 `json:"hostMask"`
    IgnoreMask     string                 `json:"ignoreMask"`
}

// SockTable type represents each line of the /cmd/[tcp|udp]
type SockTable struct {
    Id             string                 `json:"id,omitempty"`
    Timestamp      int64                  `json:"timestamp"`
    LocalAddr      SockAddr               `json:"localAddr"`
    RemoteAddr     SockAddr               `json:"remoteAddr"`
    Relation       Relation               `json:"relation"`
    Options        Options                `json:"options"`
}

// SockAddr represents
type SockAddr struct {
    IP             net.IP                 `json:"ip"`
    Name           string                 `json:"name"`
    Port           uint16                 `json:"-"`
}

type Relation struct {
    Mode           string                 `json:"mode"`
    Port           uint16                 `json:"port"`
    Command        string                 `json:"command,omitempty"`
    Result         int                    `json:"result"`
    Response       float64                `json:"response"`
    Trace          int                    `json:"trace"`
}

type Options struct {
    Service        string                 `json:"service,omitempty"`
    Status         string                 `json:"status,omitempty"`
    Command        string                 `json:"command,omitempty"`
    Timeout        float64                `json:"timeout"`
    MaxRespTime    float64                `json:"maxRespTime"`
    AccountID      uint32                 `json:"accountID"`
}

type Config struct {
    Global         *Global                `yaml:"global"`
    DB             *DB                    `yaml:"db"`
    Notifier       *Notifier              `yaml:"notifier"`
}

type Global struct {
    CertFile       string                 `yaml:"cert_file"`
    CertKey        string                 `yaml:"cert_key"`
}

type DB struct {
    Client         string                 `yaml:"client"`
    ConnString     string                 `yaml:"conn_string"`
    HistoryDays    int                    `yaml:"history_days"`
    Limit          int                    `yaml:"limit"`
    Username       string                 `yaml:"username"`
    Password       string                 `yaml:"password"`
    Bucket         string                 `yaml:"bucket"`
}

type Notifier struct {
    URLs           []string               `yaml:"urls"`
    Path           string                 `yaml:"path"`
}

type ExceptionData struct {
    Data           []Exception            `json:"data"`
}

type NetstatData struct {
    Data           []SockTable            `json:"data"`
}

func GetHash(text string) string {
    h := sha1.New()
    io.WriteString(h, text)
    return hex.EncodeToString(h.Sum(nil))
}

func GetIdRec(i *SockTable) string {
    return GetHash(fmt.Sprintf("%v:%v:%v:%v:%v:%v", i.LocalAddr.IP, i.RemoteAddr.IP, i.Relation.Mode, i.Relation.Port))
}

func GetIdExp(i *Exception) string {
    return GetHash(fmt.Sprintf("%v:%v:%v", i.AccountID, i.HostMask, i.IgnoreMask))
}

func New(filename string) (*Config, error) {

    cfg := &Config{}

    content, err := ioutil.ReadFile(filename)
    if err != nil {
       return cfg, err
    }

    if err := yaml.UnmarshalStrict(content, cfg); err != nil {
        return cfg, err
    }
    
    return cfg, nil
}