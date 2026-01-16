package main

import (
	"os"

	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

type QFlowConfig struct {
	AppId   string `yaml:"app_id"`
	ViewId  string `yaml:"view_id"`
	Cookies string `yaml:"cookies"`
}

type LarkConfig struct {
	AppId     string `yaml:"app_id"`
	AppSecret string `yaml:"app_secret"`
	AppToken  string `yaml:"app_token"`
	TableId   string `yaml:"table_id"`
}

type Config struct {
	QFlow QFlowConfig `yaml:"qflow"`
	Lark  LarkConfig  `yaml:"lark"`
}

func MustLoadConfig() *Config {
	yaml.NewEncoder(os.Stdout).Encode(Config{
		QFlow: QFlowConfig{
			AppId:   "app_id",
			ViewId:  "view_id",
			Cookies: "cookies",
		},
		Lark: LarkConfig{
			AppId:     "app_id",
			AppSecret: "app_secret",
			AppToken:  "app_token",
			TableId:   "table_id",
		},
	})

	f, err := os.Open("config.yaml")
	if err != nil {
		log.Panic().Err(err).Msg("Failed to open config file")
	}

	var cfg Config

	err = yaml.NewDecoder(f).Decode(&cfg)
	if err != nil {
		log.Panic().Err(err).Msg("Failed to decode config file")
	}

	return &cfg
}
