package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"secret2ssl/pkg/aliyun"
	"secret2ssl/pkg/config"
	"secret2ssl/pkg/kubernetes"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sclient "k8s.io/client-go/kubernetes"
)

var cfg *config.Config
var secretSyncTargets map[string][]syncTarget

type syncTarget struct {
	AliyunName string
	AliSSLName string
	Client     *aliyun.SSLClient
}

func main() {
	log.Println("Starting secret2ssl...")

	if err := config.LoadConfig("./config/config.yaml"); err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	cfg = config.GetConfig()
	if err := cfg.ValidateAndNormalize(); err != nil {
		log.Fatalf("Invalid config: %v", err)
	}

	clientset, err := kubernetes.NewClientset(cfg)
	if err != nil {
		log.Fatalf("Failed to create kubernetes client: %v", err)
	}

	aliyunConfigs := cfg.AliyunConfigs

	secretSyncTargets = make(map[string][]syncTarget)
	clientsByName := make(map[string]*aliyun.SSLClient)

	for _, item := range aliyunConfigs {
		aliyunName := item.Name

		aliyunCfg := item.Aliyun
		if err := loadAliyunCredentialFromSecret(clientset, &aliyunCfg, aliyunName); err != nil {
			log.Fatalf("Failed to load aliyun credentials for %s: %v", aliyunName, err)
		}

		client, err := aliyun.NewSSLClient(aliyunName, aliyunCfg)
		if err != nil {
			log.Fatalf("Failed to create SSL client for %s: %v", aliyunName, err)
		}
		clientsByName[aliyunName] = client

		for _, secretCfg := range item.Secrets {
			secretName := strings.TrimSpace(secretCfg.Name)
			if secretName == "" {
				log.Printf("[%s] Skip empty secret name", aliyunName)
				continue
			}
			secretNS := strings.TrimSpace(secretCfg.Namespace)
			if secretNS == "" {
				secretNS = "default"
			}
			aliSSLName := strings.TrimSpace(secretCfg.AliSSLName)
			if aliSSLName == "" {
				log.Printf("[%s] Skip %s/%s due to empty ali_ssl_name", aliyunName, secretNS, secretName)
				continue
			}

			key := fmt.Sprintf("%s/%s", secretNS, secretName)
			secretSyncTargets[key] = append(secretSyncTargets[key], syncTarget{
				AliyunName: aliyunName,
				AliSSLName: aliSSLName,
				Client:     client,
			})
		}
	}

	if len(secretSyncTargets) == 0 {
		log.Fatalf("No valid secret sync targets found in aliyun_configs")
	}

	//创建secret监控器
	monitor, err := kubernetes.NewSecretMonitor(cfg, secretChangeHandler)
	if err != nil {
		log.Fatalf("Failed to create secret monitor: %v", err)
	}

	// 启动前先做每个阿里云账号的初始补齐同步（仅阿里云不存在时上传）
	for _, item := range aliyunConfigs {
		aliyunName := item.Name
		client := clientsByName[aliyunName]
		if client == nil {
			continue
		}
		if err := client.InitialSyncMissingCertificates(monitor, item.Secrets); err != nil {
			log.Fatalf("Initial sync failed for %s: %v", aliyunName, err)
		}
	}

	//启动secret监控器
	if err := monitor.Start(); err != nil {
		log.Fatalf("Failed to start monitor: %v", err)
	}

	log.Println("secret2ssl started successfully")
	//创建一个缓冲为 1 的信号通道，用来接收系统信号
	sigChan := make(chan os.Signal, 1)
	//SIGINT：通常是 Ctrl+C
	//SIGTERM：常见于容器/进程管理器发出的终止信号
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan //阻塞主进程，等待信号

	log.Println("Shutting down secret2ssl...")
}

// secretChangeHandler 处理 Secret 变化事件，把变化的 K8s Secret 同步到阿里云证书
func secretChangeHandler(secret *corev1.Secret) {
	key := fmt.Sprintf("%s/%s", secret.Namespace, secret.Name)
	log.Printf("Handling secret change: %s", key)

	targets := secretSyncTargets[key]
	if len(targets) == 0 {
		log.Printf("No Aliyun SSL name configured for %s", key)
		return
	}

	for _, target := range targets {
		if err := target.Client.SyncSecretToSSL(secret, target.AliSSLName); err != nil {
			log.Printf("Failed to sync secret %s to Aliyun(%s) SSL %s: %v", key, target.AliyunName, target.AliSSLName, err)
			continue
		}
		log.Printf("Successfully synced secret %s to Aliyun(%s) SSL %s", key, target.AliyunName, target.AliSSLName)
	}
}

// loadAliyunCredentialFromSecret 从 Kubernetes Secret 加载阿里云凭证
func loadAliyunCredentialFromSecret(clientset *k8sclient.Clientset, aliyunCfg *config.AliyunConfig, aliyunName string) error {
	configAccessKeyID := strings.TrimSpace(aliyunCfg.AccessKeyID)
	configAccessKeySecret := strings.TrimSpace(aliyunCfg.AccessKeySecret)
	hasConfigCredential := configAccessKeyID != "" && configAccessKeySecret != ""

	secretRef := aliyunCfg.CredentialSecret
	if secretRef.Name == "" { //没有配置secret引用
		// 未配置 Secret 引用时，使用该 aliyun 配置项中的明文 AK/SK
		if !hasConfigCredential { //没有明文，返回错误
			return fmt.Errorf("aliyun credentials are empty: configure credential_secret or set access_key_id/access_key_secret in config for %s", aliyunName)
		}
		log.Printf("[%s] Aliyun credentials loaded from config file", aliyunName)
		return nil //有明文，返回成功
	}

	//从secret中获取阿里云凭证
	namespace := secretRef.Namespace
	if namespace == "" {
		namespace = "default"
	}

	accessKeyIDKey := secretRef.AccessKeyIDKey
	if accessKeyIDKey == "" {
		accessKeyIDKey = "access_key_id"
	}
	accessKeySecretKey := secretRef.AccessKeySecretKey
	if accessKeySecretKey == "" {
		accessKeySecretKey = "access_key_secret"
	}
	//获取secret
	secret, err := clientset.CoreV1().Secrets(namespace).Get(
		context.Background(),
		secretRef.Name,
		metav1.GetOptions{},
	)
	if err != nil {
		// Secret 不可用时，若配置文件里有明文，自动回退，避免启动失败
		if hasConfigCredential {
			aliyunCfg.AccessKeyID = configAccessKeyID
			aliyunCfg.AccessKeySecret = configAccessKeySecret
			log.Printf("[%s] Failed to read secret %s/%s, fallback to config file credentials: %v", aliyunName, namespace, secretRef.Name, err)
			return nil
		}
		return fmt.Errorf("failed to read secret %s/%s: %v", namespace, secretRef.Name, err)
	}
	//从secret中获取阿里云key凭证
	accessKeyID, ok := secret.Data[accessKeyIDKey]
	if !ok {
		if hasConfigCredential {
			aliyunCfg.AccessKeyID = configAccessKeyID
			aliyunCfg.AccessKeySecret = configAccessKeySecret
			log.Printf("[%s] Secret %s/%s missing key %q, fallback to config file credentials", aliyunName, namespace, secretRef.Name, accessKeyIDKey)
			return nil
		}
		return fmt.Errorf("secret %s/%s missing key %q", namespace, secretRef.Name, accessKeyIDKey)
	}
	//从secret中获取阿里云secret凭证
	accessKeySecret, ok := secret.Data[accessKeySecretKey]
	if !ok {
		if hasConfigCredential {
			aliyunCfg.AccessKeyID = configAccessKeyID
			aliyunCfg.AccessKeySecret = configAccessKeySecret
			log.Printf("[%s] Secret %s/%s missing key %q, fallback to config file credentials", aliyunName, namespace, secretRef.Name, accessKeySecretKey)
			return nil
		}
		return fmt.Errorf("secret %s/%s missing key %q", namespace, secretRef.Name, accessKeySecretKey)
	}
	//覆盖config中的明文凭证
	aliyunCfg.AccessKeyID = strings.TrimSpace(string(accessKeyID))
	aliyunCfg.AccessKeySecret = strings.TrimSpace(string(accessKeySecret))
	if aliyunCfg.AccessKeyID == "" || aliyunCfg.AccessKeySecret == "" {
		if hasConfigCredential {
			aliyunCfg.AccessKeyID = configAccessKeyID
			aliyunCfg.AccessKeySecret = configAccessKeySecret
			log.Printf("[%s] Secret %s/%s contains empty credentials, fallback to config file credentials", aliyunName, namespace, secretRef.Name)
			return nil
		}
		return fmt.Errorf("secret %s/%s contains empty aliyun credentials", namespace, secretRef.Name)
	}

	log.Printf("[%s] Aliyun credentials loaded from secret %s/%s", aliyunName, namespace, secretRef.Name)
	return nil
}
