package main

import (
	"encoding/json"
	"os"

	"github.com/rs/zerolog/log"
)

type QFlowConfig struct {
	AppId  string `json:"app_id"`
	ViewId string `json:"view_id"`
}

type LarkConfig struct {
	AppId     string `json:"app_id"`
	AppSecret string `json:"app_secret"`
	AppToken  string `json:"app_token"`
	TableId   string `json:"table_id"`
}

type Config struct {
	QFlow    QFlowConfig `json:"qflow"`
	Lark     LarkConfig  `json:"lark"`
	Interval string      `json:"interval"`
}

func MustLoadConfig() *Config {
	f, err := os.Open("config.json")
	if err != nil {
		log.Panic().Err(err).Msg("Failed to open config file")
	}

	var cfg Config

	err = json.NewDecoder(f).Decode(&cfg)
	if err != nil {
		log.Panic().Err(err).Msg("Failed to decode config")
	}

	return &cfg
}
