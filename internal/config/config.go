package config

import (
	"fmt"
	"io"
	"log"
	"os"

	"crypto/sha1"
	"encoding/hex"
	"io/ioutil"
	"strings"

	"gopkg.in/yaml.v2"
)

type RecArgs struct {
	Id        string `json:"id,omitempty"`
	Type      string `json:"type,omitempty"`
	SrcName   string `json:"src_name,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`

	RelationPort       string `json:"relation_port,omitempty"`
	RelationType       string `json:"relation_type,omitempty"`
	RelationMode       string `json:"relation_mode,omitempty"`
	RelationResult     string `json:"relation_result,omitempty"`
	RelationTrace      string `json:"relation_trace,omitempty"`
	RelationPing       string `json:"relation_ping,omitempty"`
	RelationPacketLoss string `json:"relation_packet_loss,omitempty"`
	RelationMinRtt     string `json:"relation_min_rtt,omitempty"`
	RelationMaxRtt     string `json:"relation_max_rtt,omitempty"`
	RelationAvgRtt     string `json:"relation_avg_rtt,omitempty"`

	OptionsService      string `json:"options_service,omitempty"`
	OptionsStatus       string `json:"options_status,omitempty"`
	OptionsAccountId    string `json:"options_accountid,omitempty"`
	OptionsSrcInfo      string `json:"options_src_info,omitempty"`
	OptionsDstInfo      string `json:"options_dst_info,omitempty"`
	OptionsDescriptions string `json:"options_descriptions,omitempty"`

	LocalAddrName string `json:"local_addr_name,omitempty"`
	LocalAddrIp   string `json:"local_addr_ip,omitempty"`
	LocalAddrPort string `json:"local_addr_port,omitempty"`

	RemoteAddrName string `json:"remote_addr_name,omitempty"`
	RemoteAddrIp   string `json:"remote_addr_ip,omitempty"`
	RemoteAddrPort string `json:"remote_addr_port,omitempty"`
}

type ExpArgs struct {
	Id        string
	SrcName   string
	AccountID string
}

type Exception struct {
	Id         string `json:"id,omitempty"`
	Timestamp  int64  `json:"timestamp,omitempty"`
	AccountID  uint32 `json:"accountID"`
	HostMask   string `json:"hostMask"`
	IgnoreMask string `json:"ignoreMask"`
}

type SockTable struct {
	Id         string   `json:"id,omitempty"`
	State      string   `json:"-"`
	Timestamp  int64    `json:"timestamp,omitempty"`
	LocalAddr  SockAddr `json:"localAddr"`
	RemoteAddr SockAddr `json:"remoteAddr"`
	Relation   Relation `json:"relation"`
	Options    Options  `json:"options"`
}

type SockAddr struct {
	IP   string `json:"ip" bson:"ip"`
	Name string `json:"name" bson:"name"`
	Port uint32 `json:"port,omitempty" bson:"port,omitempty"`
}

type Relation struct {
	Mode       string  `json:"mode" bson:"mode"`
	Type       string  `json:"type,omitempty" bson:"type,omitempty"`
	Port       uint32  `json:"port,omitempty" bson:"port,omitempty"`
	Command    string  `json:"command,omitempty" bson:"command,omitempty"`
	Result     int32   `json:"result" bson:"result"`
	Response   float32 `json:"response" bson:"response"`
	Trace      int32   `json:"trace" bson:"trace"`
	Ping       int32   `json:"ping,omitempty" bson:"ping,omitempty"`
	Packets    int32   `json:"packets,omitempty" bson:"packets,omitempty"`
	PacketLoss int32   `json:"packet_loss,omitempty" bson:"packet_loss,omitempty"`
	MinRtt     float32 `json:"min_rtt,omitempty" bson:"min_rtt,omitempty"`
	MaxRtt     float32 `json:"max_rtt,omitempty" bson:"max_rtt,omitempty"`
	AvgRtt     float32 `json:"avg_rtt,omitempty" bson:"avg_rtt,omitempty"`
}

type Options struct {
	Service      string  `json:"service,omitempty" bson:"service,omitempty"`
	Status       string  `json:"status,omitempty" bson:"status,omitempty"`
	Command      string  `json:"command,omitempty" bson:"command,omitempty"`
	Timeout      float32 `json:"timeout,omitempty" bson:"timeout,omitempty"`
	MaxRespTime  float32 `json:"maxRespTime,omitempty" bson:"maxRespTime,omitempty"`
	AccountID    uint32  `json:"accountID,omitempty" bson:"accountID,omitempty"`
	HostMask     string  `json:"hostMask,omitempty" bson:"hostMask,omitempty"`
	IgnoreMask   string  `json:"ignoreMask,omitempty" bson:"ignoreMask,omitempty"`
	SrcInfo      string  `json:"src_info,omitempty" bson:"src_info,omitempty"`
	DstInfo      string  `json:"dst_info,omitempty" bson:"dst_info,omitempty"`
	Descriptions string  `json:"descriptions,omitempty" bson:"descriptions,omitempty"`
}

type Config struct {
	Global    *Global    `yaml:"global"`
	DB        *DB        `yaml:"db"`
	Notifier  *Notifier  `yaml:"notifier"`
	Collector *Collector `yaml:"collector"`
}

type Global struct {
	CertFile string    `yaml:"cert_file"`
	CertKey  string    `yaml:"cert_key"`
	Users    GlobUsers `yaml:"users"`
}

type GlobUsers map[string]string

type UserInfo struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type DB struct {
	Client      string `yaml:"client"`
	ConnString  string `yaml:"conn_string"`
	HistoryDays int    `yaml:"history_days"`
	Limit       int    `yaml:"limit"`
	Bucket      string `yaml:"bucket"`
	Host        string `yaml:"host"`
	Name        string `yaml:"name"`
	Username    string `yaml:"username"`
	Password    string `yaml:"password"`
}

type Notifier struct {
	URLs     []string `yaml:"urls"`
	Path     string   `yaml:"path"`
	Username string   `yaml:"username"`
	Password string   `yaml:"password"`
}

type Collector struct {
	URLs     []string `yaml:"urls"`
	Path     string   `yaml:"path"`
	Prepare  bool     `yaml:"prepare"`
	Username string   `yaml:"username"`
	Password string   `yaml:"password"`
}

type ExceptionData struct {
	Data []Exception `json:"data"`
}

type RecordsData struct {
	Data []SockTable `json:"data"`
}

type NetstatData struct {
	Data []SockTable `json:"data"`
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
	var users []UserInfo
	if err := unmarshal(&users); err != nil {
		return err
	}

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
