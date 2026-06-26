package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v2"
)

type SecretConfig struct {
	Name       string `yaml:"name"`
	Namespace  string `yaml:"namespace"`
	AliSSLName string `yaml:"ali_ssl_name"`
}

type AliyunConfig struct {
	AccessKeyID         string                 `yaml:"access_key_id"`
	AccessKeySecret     string                 `yaml:"access_key_secret"`
	Region              string                 `yaml:"region"`
	SSLEndpoint         string                 `yaml:"ssl_endpoint"`
	SSLInternalEndpoint string                 `yaml:"ssl_internal_endpoint"`
	UseInternalEndpoint bool                   `yaml:"use_internal_endpoint"`
	CredentialSecret    CredentialSecretConfig `yaml:"credential_secret"`
}

type CredentialSecretConfig struct {
	Namespace          string `yaml:"namespace"`
	Name               string `yaml:"name"`
	AccessKeyIDKey     string `yaml:"access_key_id_key"`
	AccessKeySecretKey string `yaml:"access_key_secret_key"`
}

type KubernetesConfig struct {
	Kubeconfig     string `yaml:"kubeconfig"`
	WatchNamespace string `yaml:"watch_namespace"`
	ResyncPeriod   int    `yaml:"resync_period"`
	InCluster      bool   `yaml:"in_cluster"`
}

type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

type Config struct {
	Secrets    []SecretConfig   `yaml:"secrets"`
	Aliyun     AliyunConfig     `yaml:"aliyun"`
	Kubernetes KubernetesConfig `yaml:"kubernetes"`
	Logging    LoggingConfig    `yaml:"logging"`
}

var cfg *Config

func LoadConfig(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read config file: %v", err)
	}

	cfg = &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("failed to parse config file: %v", err)
	}

	return nil
}

func GetConfig() *Config {
	return cfg
}
