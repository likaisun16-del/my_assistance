package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server struct {
		Addr string `yaml:"addr"`
	} `yaml:"server"`
	Postgres struct {
		DSN string `yaml:"dsn"`
	} `yaml:"postgres"`
	JWT struct {
		Secret  string `yaml:"secret"`
		TTLHour int    `yaml:"ttl_hour"`
	} `yaml:"jwt"`
	Agent struct {
		BaseURL string `yaml:"base_url"`
		Timeout int    `yaml:"timeout"`
	} `yaml:"agent"`
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, nil
}
