package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v2"
)

// SecretConfig 需要同步的tls secret配置
type SecretConfig struct {
	Name       string `yaml:"name"`         //需要同步的tls secret名称
	Namespace  string `yaml:"namespace"`    //需要同步的tls secret所属命名空间
	AliSSLName string `yaml:"ali_ssl_name"` //需要同步的tls secret在阿里云SSL中的名称
}

// AliyunConfig 阿里云CAS配置
type AliyunConfig struct {
	AccessKeyID         string                 `yaml:"access_key_id"`         //阿里云凭证access_key_id
	AccessKeySecret     string                 `yaml:"access_key_secret"`     //阿里云凭证access_key_secret
	Region              string                 `yaml:"region"`                //阿里云地域
	SSLEndpoint         string                 `yaml:"ssl_endpoint"`          //阿里云SSL外网endpoint
	SSLInternalEndpoint string                 `yaml:"ssl_internal_endpoint"` //阿里云SSL内网endpoint
	UseInternalEndpoint bool                   `yaml:"use_internal_endpoint"` //是否使用内网endpoint
	CredentialSecret    CredentialSecretConfig `yaml:"credential_secret"`     //阿里云凭证secret配置
}

// CredentialSecretConfig 阿里云凭证secret配置
type CredentialSecretConfig struct {
	Namespace          string `yaml:"namespace"`             //阿里云凭证secret所属命名空间
	Name               string `yaml:"name"`                  //阿里云凭证secret名称
	AccessKeyIDKey     string `yaml:"access_key_id_key"`     //阿里云凭证secret中的access_key_id key
	AccessKeySecretKey string `yaml:"access_key_secret_key"` //阿里云凭证secret中的access_key_secret key
}

// KubernetesConfig Kubernetes配置
type KubernetesConfig struct {
	Kubeconfig   string `yaml:"kubeconfig"`    //kubeconfig文件路径
	ResyncPeriod int    `yaml:"resync_period"` //重新同步周期
	InCluster    bool   `yaml:"in_cluster"`    //是否使用in-cluster模式
}

// LoggingConfig 日志配置
type LoggingConfig struct {
	Level  string `yaml:"level"`  //日志级别
	Format string `yaml:"format"` //日志格式
}

// Config 配置
type Config struct {
	Secrets    []SecretConfig   `yaml:"secrets"`    //需要同步的tls secret配置
	Aliyun     AliyunConfig     `yaml:"aliyun"`     //阿里云CAS配置
	Kubernetes KubernetesConfig `yaml:"kubernetes"` //Kubernetes配置
	Logging    LoggingConfig    `yaml:"logging"`    //日志配置
}

var cfg *Config

// LoadConfig 加载配置
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

// GetConfig 获取配置
func GetConfig() *Config {
	return cfg
}
