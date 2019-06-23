package config

import "github.com/spf13/viper"

type AppConfig struct {
	PORT                  string
	AuthorizeDotNetConfig struct {
		APILoginId string
		TransactionKey string
		Endpoint  string
	}
	VantivConfig struct {
		Endpoint  string
	}
}

func InitConfig() (AppConfig, error) {
	viper.SetConfigFile("./config/app-config.json")
	err := viper.ReadInConfig()

	if err != nil {
		return AppConfig{}, err
	}

	var appConfig AppConfig
	err = viper.Unmarshal(&appConfig)

	return appConfig, err
}
