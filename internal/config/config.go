package config

import (
    "io/ioutil"
    "gopkg.in/yaml.v2"
    "net"
)

type RecArgs struct {
    Id             string
    SrcName        string
    Timestamp      int64
    Type           string
}

type ExpArgs struct {
    Id             string
    SrcName        string
    AccountID      uint32
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
    Type           string                 `json:"type"`
    Timestamp      int64                  `json:"time"`
    LocalAddr      SockAddr               `json:"localAddr"`
    RemoteAddr     SockAddr               `json:"remoteAddr"`
    Relation       Relation               `json:"relation"`
    Options        Options                `json:"options"`
}

// SockAddr represents
type SockAddr struct {
    IP             net.IP                 `json:"ip"`
    Name           string                 `json:"name"`
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
    FlushInterval  string                 `yaml:"flush_interval"`
}

type Notifier struct {
    URLs           []string               `yaml:"urls"`
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