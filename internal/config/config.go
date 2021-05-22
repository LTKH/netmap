package config

import (
    "io/ioutil"
	"gopkg.in/yaml.v2"
)

type Config struct {
	Global           *Global            `yaml:"global"`
    Databases        []*Database        `yaml:"databases"`
	Alerting         *Alerting          `yaml:"alerting"`
}

type Global struct {
	Listen           string             `yaml:"listen_address"`
	Cert_file        string             `yaml:"cert_file"`
	Cert_key         string             `yaml:"cert_key"`
}

type Database struct {
    Uri              string             `yaml:"uri"`
	UserName         string             `yaml:"username"`
	Password         string             `yaml:"password"`
}

type Alerting struct {
    Alertmanagers    []Alertmanager     `yaml:"alertmanagers"`
}

type Alertmanager struct {
    StaticConfigs    []StaticConfig     `yaml:"static_configs"`
}

type StaticConfig struct {
    Targets          []string           `yaml:"targets"`
}

func LoadConfigFile(filename string) (*Config, error) {
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