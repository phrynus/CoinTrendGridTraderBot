package main

import (
	"encoding/json"
	"log"
	"os"
)

var config Config

type Config struct {
	APIKey    string `json:"api_key"`
	SecretKey string `json:"secret_key"`
	Proxy     string `json:"proxy"`
	Symbol    string `json:"symbol"`
	Direction string `json:"direction"`
}

func init() {
	b, err := os.ReadFile("config.json")
	if err != nil {
		log.Fatal(err)
	}
	err = json.Unmarshal(b, &config)
	if err != nil {
		log.Fatal(err)
	}
}
