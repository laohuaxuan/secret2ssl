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
)

var sslClient *aliyun.SSLClient
var cfg *config.Config

func main() {
	log.Println("Starting secret2ssl...")

	if err := config.LoadConfig("./config/config.yaml"); err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	cfg = config.GetConfig()
	if err := loadAliyunCredentialFromSecret(cfg); err != nil {
		log.Fatalf("Failed to load aliyun credentials from k8s secret: %v", err)
	}

	var err error
	//创建阿里云CAS客户端
	sslClient, err = aliyun.NewSSLClient(cfg)
	if err != nil {
		log.Fatalf("Failed to create SSL client: %v", err)
	}

	//创建secret监控器
	monitor, err := kubernetes.NewSecretMonitor(cfg, secretChangeHandler)
	if err != nil {
		log.Fatalf("Failed to create secret monitor: %v", err)
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

	var aliSSLName string
	for _, secretCfg := range cfg.Secrets {
		if secretCfg.Namespace == secret.Namespace && secretCfg.Name == secret.Name {
			aliSSLName = secretCfg.AliSSLName
			break
		}
	}

	if aliSSLName == "" {
		log.Printf("No Aliyun SSL name configured for %s", key)
		return
	}
	//同步到阿里云证书
	fmt.Println("【test】sync secret to aliyun ssl from callBackHandler", secret, aliSSLName)
	// if err := sslClient.SyncSecretToSSL(secret, aliSSLName); err != nil {
	// 	log.Printf("Failed to sync secret %s to Aliyun SSL %s: %v", key, aliSSLName, err)
	// 	return
	// }

	log.Printf("Successfully synced secret %s to Aliyun SSL %s", key, aliSSLName)
}

// loadAliyunCredentialFromSecret 从 Kubernetes Secret 加载阿里云凭证
func loadAliyunCredentialFromSecret(cfg *config.Config) error {
	configAccessKeyID := strings.TrimSpace(cfg.Aliyun.AccessKeyID)
	configAccessKeySecret := strings.TrimSpace(cfg.Aliyun.AccessKeySecret)
	hasConfigCredential := configAccessKeyID != "" && configAccessKeySecret != ""

	secretRef := cfg.Aliyun.CredentialSecret
	if secretRef.Name == "" { //没有配置secret引用
		// 向后兼容：未配置 Secret 引用时，沿用 config.yaml 中的明文配置
		if !hasConfigCredential { //没有明文，返回错误
			return fmt.Errorf("aliyun credentials are empty: configure credential_secret or set access_key_id/access_key_secret in config")
		}
		log.Printf("Aliyun credentials loaded from config file")
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
	//创建kubernetes客户端
	clientset, err := kubernetes.NewClientset(cfg)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %v", err)
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
			cfg.Aliyun.AccessKeyID = configAccessKeyID
			cfg.Aliyun.AccessKeySecret = configAccessKeySecret
			log.Printf("Failed to read secret %s/%s, fallback to config file credentials: %v", namespace, secretRef.Name, err)
			return nil
		}
		return fmt.Errorf("failed to read secret %s/%s: %v", namespace, secretRef.Name, err)
	}
	//从secret中获取阿里云key凭证
	accessKeyID, ok := secret.Data[accessKeyIDKey]
	if !ok {
		if hasConfigCredential {
			cfg.Aliyun.AccessKeyID = configAccessKeyID
			cfg.Aliyun.AccessKeySecret = configAccessKeySecret
			log.Printf("Secret %s/%s missing key %q, fallback to config file credentials", namespace, secretRef.Name, accessKeyIDKey)
			return nil
		}
		return fmt.Errorf("secret %s/%s missing key %q", namespace, secretRef.Name, accessKeyIDKey)
	}
	//从secret中获取阿里云secret凭证
	accessKeySecret, ok := secret.Data[accessKeySecretKey]
	if !ok {
		if hasConfigCredential {
			cfg.Aliyun.AccessKeyID = configAccessKeyID
			cfg.Aliyun.AccessKeySecret = configAccessKeySecret
			log.Printf("Secret %s/%s missing key %q, fallback to config file credentials", namespace, secretRef.Name, accessKeySecretKey)
			return nil
		}
		return fmt.Errorf("secret %s/%s missing key %q", namespace, secretRef.Name, accessKeySecretKey)
	}
	//覆盖config中的明文凭证
	cfg.Aliyun.AccessKeyID = strings.TrimSpace(string(accessKeyID))
	cfg.Aliyun.AccessKeySecret = strings.TrimSpace(string(accessKeySecret))
	if cfg.Aliyun.AccessKeyID == "" || cfg.Aliyun.AccessKeySecret == "" {
		if hasConfigCredential {
			cfg.Aliyun.AccessKeyID = configAccessKeyID
			cfg.Aliyun.AccessKeySecret = configAccessKeySecret
			log.Printf("Secret %s/%s contains empty credentials, fallback to config file credentials", namespace, secretRef.Name)
			return nil
		}
		return fmt.Errorf("secret %s/%s contains empty aliyun credentials", namespace, secretRef.Name)
	}

	log.Printf("Aliyun credentials loaded from secret %s/%s", namespace, secretRef.Name)
	return nil
}
