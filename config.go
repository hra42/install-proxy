package main

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Tool struct {
	Host        string `yaml:"host"`
	Repo        string `yaml:"repo"`
	DisplayName string `yaml:"display_name"`
}

type Config struct {
	Listen string `yaml:"listen"`
	Tools  []Tool `yaml:"tools"`

	byHost map[string]Tool
}

func LoadBytes(data []byte, source string) (*Config, error) {
	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", source, err)
	}

	if strings.TrimSpace(cfg.Listen) == "" {
		cfg.Listen = ":8080"
	}

	if len(cfg.Tools) == 0 {
		return nil, fmt.Errorf("config %s: no tools defined", source)
	}

	cfg.byHost = make(map[string]Tool, len(cfg.Tools))
	for i, t := range cfg.Tools {
		host := strings.ToLower(strings.TrimSpace(t.Host))
		repo := strings.TrimSpace(t.Repo)
		if host == "" {
			return nil, fmt.Errorf("tool[%d]: missing host", i)
		}
		if repo == "" {
			return nil, fmt.Errorf("tool[%d]: missing repo", i)
		}
		if _, dup := cfg.byHost[host]; dup {
			return nil, fmt.Errorf("duplicate host %q", host)
		}
		cfg.byHost[host] = t
	}

	return cfg, nil
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	return LoadBytes(data, path)
}

func Resolve(flagPath string, flagChanged bool) (*Config, string, error) {
	if inline := strings.TrimSpace(os.Getenv("INSTALL_PROXY_CONFIG")); inline != "" {
		cfg, err := LoadBytes([]byte(inline), "$INSTALL_PROXY_CONFIG")
		return cfg, "$INSTALL_PROXY_CONFIG", err
	}

	path := flagPath
	if !flagChanged {
		if env := strings.TrimSpace(os.Getenv("CONFIG_PATH")); env != "" {
			path = env
		}
	}

	cfg, err := Load(path)
	return cfg, path, err
}

func (c *Config) Lookup(host string) (Tool, bool) {
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	host = strings.ToLower(strings.TrimSpace(host))
	t, ok := c.byHost[host]
	return t, ok
}
