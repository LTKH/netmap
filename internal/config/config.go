package config

import (
    "io/ioutil"
	"gopkg.in/yaml.v2"
    //"net/url"
)

type Config struct {
	Global           *Global            `yaml:"global"`
    Cache            *Cache             `yaml:"cache"`
    DB               *DB                `yaml:"db"`
    Cluster          *Cluster           `yaml:"cluster"`
    Notifier         *Notifier          `yaml:"notifier"`
}

type Global struct {
	Listen           string             `yaml:"listen_address"`
	CertFile         string             `yaml:"cert_file"`
	CertKey          string             `yaml:"cert_key"`
}

type DB struct {
    Client           string             `yaml:"client"`
    ConnString       string             `yaml:"conn_string"`
    HistoryDays      int                `yaml:"history_days"`
}

type Cache struct {
    Limit          int                  `yaml:"limit"`
    FlushInterval  string               `yaml:"flush_interval"`
}

type Cluster struct {
    URLs            []string            `yaml:"urls"`
}

type Notifier struct {
    URLs            []string            `yaml:"urls"`
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