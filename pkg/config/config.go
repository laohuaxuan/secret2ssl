package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v2"
)

// SecretConfig 需要同步的tls secret配置
type SecretConfig struct {
	Name       string `yaml:"name"`         //需要同步的tls secret名称
	Namespace  string `yaml:"namespace"`    //需要同步的tls secret所属命名空间
	AliSSLName string `yaml:"ali_ssl_name"` //需要同步的tls secret在阿里云SSL中的名称
}

// AliyunSyncConfig 一个阿里云账号及其需要同步的 secrets 列表
type AliyunSyncConfig struct {
	Name    string         `yaml:"name"`    //配置项名称，仅用于日志区分
	Aliyun  AliyunConfig   `yaml:"aliyun"`  //阿里云账号配置
	Secrets []SecretConfig `yaml:"secrets"` //该账号下需要同步的 secrets
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
	// 支持多个 aliyun 账号，每个账号下可配置多个 secret
	AliyunConfigs []AliyunSyncConfig `yaml:"aliyun_configs"`
	// 通用配置
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

// ValidateAndNormalize 启动时配置校验（逐个 aliyun 配置项）
func (c *Config) ValidateAndNormalize() error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	if len(c.AliyunConfigs) == 0 {
		return fmt.Errorf("aliyun_configs is required and cannot be empty")
	}
	if !c.Kubernetes.InCluster && strings.TrimSpace(c.Kubernetes.Kubeconfig) == "" {
		return fmt.Errorf("kubernetes.kubeconfig is required when in_cluster=false")
	}
	if c.Kubernetes.ResyncPeriod <= 0 {
		return fmt.Errorf("kubernetes.resync_period must be greater than 0")
	}

	nameSet := make(map[string]bool)
	for i := range c.AliyunConfigs {
		item := &c.AliyunConfigs[i]
		item.Name = strings.TrimSpace(item.Name)
		if item.Name == "" {
			return fmt.Errorf("aliyun_configs[%d].name is required", i)
		}
		if nameSet[item.Name] {
			return fmt.Errorf("aliyun_configs[%d].name is duplicated: %s", i, item.Name)
		}
		nameSet[item.Name] = true

		item.Aliyun.AccessKeyID = strings.TrimSpace(item.Aliyun.AccessKeyID)
		item.Aliyun.AccessKeySecret = strings.TrimSpace(item.Aliyun.AccessKeySecret)
		item.Aliyun.Region = strings.TrimSpace(item.Aliyun.Region)
		if item.Aliyun.Region == "" {
			return fmt.Errorf("aliyun_configs[%d](%s).aliyun.region is required", i, item.Name)
		}

		hasPlainAKSK := item.Aliyun.AccessKeyID != "" || item.Aliyun.AccessKeySecret != ""
		if hasPlainAKSK && (item.Aliyun.AccessKeyID == "" || item.Aliyun.AccessKeySecret == "") {
			return fmt.Errorf("aliyun_configs[%d](%s).aliyun access_key_id/access_key_secret must be both set", i, item.Name)
		}
		item.Aliyun.CredentialSecret.Name = strings.TrimSpace(item.Aliyun.CredentialSecret.Name)
		hasSecretRef := item.Aliyun.CredentialSecret.Name != ""
		if !hasPlainAKSK && !hasSecretRef {
			return fmt.Errorf("aliyun_configs[%d](%s) must configure either plain AKSK or aliyun.credential_secret", i, item.Name)
		}

		if len(item.Secrets) == 0 {
			return fmt.Errorf("aliyun_configs[%d](%s).secrets is required and cannot be empty", i, item.Name)
		}
		seenSecret := make(map[string]bool)
		for j := range item.Secrets {
			secret := &item.Secrets[j]
			secret.Name = strings.TrimSpace(secret.Name)
			secret.Namespace = strings.TrimSpace(secret.Namespace)
			secret.AliSSLName = strings.TrimSpace(secret.AliSSLName)
			if secret.Name == "" {
				return fmt.Errorf("aliyun_configs[%d](%s).secrets[%d].name is required", i, item.Name, j)
			}
			if secret.Namespace == "" {
				secret.Namespace = "default"
			}
			if secret.AliSSLName == "" {
				return fmt.Errorf("aliyun_configs[%d](%s).secrets[%d].ali_ssl_name is required", i, item.Name, j)
			}
			key := secret.Namespace + "/" + secret.Name + "->" + secret.AliSSLName
			if seenSecret[key] {
				return fmt.Errorf("aliyun_configs[%d](%s).secrets[%d] is duplicated: %s", i, item.Name, j, key)
			}
			seenSecret[key] = true
		}
	}

	return nil
}

// AllSecrets 返回需要监听的 secret 列表（跨所有 aliyun 配置去重）
func (c *Config) AllSecrets() []SecretConfig {
	seen := make(map[string]bool)
	result := make([]SecretConfig, 0)

	for _, item := range c.AliyunConfigs {
		for _, secret := range item.Secrets {
			namespace := strings.TrimSpace(secret.Namespace)
			if namespace == "" {
				namespace = "default"
			}
			name := strings.TrimSpace(secret.Name)
			if name == "" {
				continue
			}
			key := namespace + "/" + name
			if seen[key] {
				continue
			}
			seen[key] = true
			result = append(result, SecretConfig{
				Name:      name,
				Namespace: namespace,
			})
		}
	}

	return result
}
