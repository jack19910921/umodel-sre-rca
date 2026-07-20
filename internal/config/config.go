package config

import (
	"errors"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	ListenAddr    string `yaml:"listen_addr"`
	CallbackToken string `yaml:"callback_token"`
	StateDB       string `yaml:"state_db"`
	Aliyun        struct {
		Profile   string `yaml:"profile"`
		Workspace string `yaml:"workspace"`
		Region    string `yaml:"region"`
	} `yaml:"aliyun"`
	Feishu struct {
		AppID     string `yaml:"app_id"`
		AppSecret string `yaml:"app_secret"`
		ChatID    string `yaml:"chat_id"`
	} `yaml:"feishu"`
	Worker struct {
		MaxTurns       int `yaml:"max_turns"`
		TimeoutSeconds int `yaml:"timeout_seconds"`
	} `yaml:"worker"`
}

func Load(path string) (Config, error) {
	var cfg Config
	raw, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return cfg, err
	}
	if cfg.CallbackToken == "" {
		return cfg, errors.New("callback_token is required")
	}
	if cfg.StateDB == "" || cfg.Aliyun.Profile == "" || cfg.Worker.MaxTurns < 1 {
		return cfg, errors.New("state_db, aliyun.profile, and worker.max_turns are required")
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = "127.0.0.1:8080"
	}
	if cfg.Worker.TimeoutSeconds < 1 {
		cfg.Worker.TimeoutSeconds = 300
	}
	return cfg, nil
}
