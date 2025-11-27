package config

import (
    "io"
    "os"
    "fmt"
    "log"
    //"net"
    //"time"
    "strings"
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
    Id             string                  `json:"id,omitempty"`
    Timestamp      int64                   `json:"timestamp,omitempty"`
    AccountID      uint32                  `json:"accountID"`
    HostMask       string                  `json:"hostMask"`
    IgnoreMask     string                  `json:"ignoreMask"`
}

// SockTable type represents each line of the /cmd/[tcp|udp]
type SockTable struct {
    Id             string                  `json:"id,omitempty"`
    State          string                  `json:"-"`
    Timestamp      int64                   `json:"timestamp,omitempty"`
    LocalAddr      SockAddr                `json:"localAddr"`
    RemoteAddr     SockAddr                `json:"remoteAddr"`
    Relation       Relation                `json:"relation"`
    Options        Options                 `json:"options"`
}

// SockAddr represents
type SockAddr struct {
    IP             string                  `json:"ip"`
    Name           string                  `json:"name"`
    Port           uint32                  `json:"port,omitempty"`
}

type Relation struct {
    Mode           string                  `json:"mode"`
    Type           string                  `json:"type"`
    Port           uint32                  `json:"port,omitempty"`
    Command        string                  `json:"command,omitempty"`
    Result         int32                   `json:"result"`
    Response       float32                 `json:"response"`
    Trace          int32                   `json:"trace"`
}

type Options struct {
    Service        string                  `json:"service,omitempty"`
    Status         string                  `json:"status,omitempty"`
    Command        string                  `json:"command,omitempty"`
    Timeout        float32                 `json:"timeout"`
    MaxRespTime    float32                 `json:"maxRespTime"`
    AccountID      uint32                  `json:"accountID"`
    HostMask       string                  `json:"-"`
    IgnoreMask     string                  `json:"-"`
}

type Config struct {
    Global         *Global                 `yaml:"global"`
    DB             *DB                     `yaml:"db"`
    Notifier       *Notifier               `yaml:"notifier"`
    Collector      *Collector              `yaml:"collector"`
}

type Global struct {
    CertFile       string                  `yaml:"cert_file"`
    CertKey        string                  `yaml:"cert_key"`
    Users          GlobUsers               `yaml:"users"`
}

type GlobUsers map[string]string

type UserInfo struct {
    Username       string                  `yaml:"username"`
    Password       string                  `yaml:"password"`
}

type DB struct {
    Client         string                  `yaml:"client"`
    ConnString     string                  `yaml:"conn_string"`
    HistoryDays    int                     `yaml:"history_days"`
    Limit          int                     `yaml:"limit"`
    Bucket         string                  `yaml:"bucket"`
    Host           string                  `yaml:"host"`
    Name           string                  `yaml:"name"`
    Username       string                  `yaml:"username"`
    Password       string                  `yaml:"password"`
}

type Notifier struct {
    URLs           []string                `yaml:"urls"`
    Path           string                  `yaml:"path"`
    Username       string                  `yaml:"username"`
    Password       string                  `yaml:"password"`
}

type Collector struct {
    URLs           []string                `yaml:"urls"`
    Path           string                  `yaml:"path"`
    Prepare        bool                    `yaml:"prepare"`
    Username       string                  `yaml:"username"`
    Password       string                  `yaml:"password"`
}

type ExceptionData struct {
    Data           []Exception             `json:"data"`
}

type RecordsData struct {
    Data           []SockTable             `json:"data"`
}

type NetstatData struct {
    Data           []SockTable             `json:"data"`
}

func getEnv(value string) string {
    if len(value) > 0 && string(value[0]) == "$" {
        val, ok := os.LookupEnv(strings.TrimPrefix(value, "$"))
        if !ok {
            log.Fatalf("[error] no value found for %v", value)
            return ""
        }
        return val
    }

    return value
}

func (u *GlobUsers) UnmarshalYAML(unmarshal func(interface{}) error) error {
    // Временная структура для чтения массива
    var users []UserInfo
    if err := unmarshal(&users); err != nil {
        return err
    }

    // Создаем карту и заполняем её
    result := make(map[string]string)
    for _, usr := range users {
        usr.Username = getEnv(usr.Username)
        usr.Password = getEnv(usr.Password)
        result[usr.Username] = usr.Password
    }
    *u = result
    return nil
}

func GetHash(text string) string {
    h := sha1.New()
    io.WriteString(h, text)
    return hex.EncodeToString(h.Sum(nil))
}

func GetIdRec(i *SockTable) string {
    return GetHash(fmt.Sprintf("%v:%v:%v:%v", i.LocalAddr.IP, i.RemoteAddr.IP, i.Relation.Mode, i.Relation.Port))
}

func GetIdExp(i *Exception) string {
    return GetHash(fmt.Sprintf("%v:%v:%v", i.AccountID, i.HostMask, i.IgnoreMask))
}

func New(filename *string) (*Config, error) {

    cfg := &Config{}

    content, err := ioutil.ReadFile(*filename)
    if err != nil {
       return cfg, err
    }

    if err := yaml.UnmarshalStrict(content, cfg); err != nil {
        return cfg, err
    }

    if cfg.Notifier == nil {
        cfg.Notifier = &Notifier{}
    }

    if cfg.Collector == nil {
        cfg.Collector = &Collector{}
    }

    if cfg.Notifier.Path == "" {
        cfg.Notifier.Path = "/api/v1/alerts"
    }

    if cfg.Collector.Path == "" {
        cfg.Collector.Path = "/api/v1/records"
    }
    
    return cfg, nil
}