package aliyun

import (
	"crypto/tls"
	"fmt"
	"log"
	"strconv"
	"strings"

	"secret2ssl/pkg/config"
	"secret2ssl/pkg/kubernetes"

	cas "github.com/alibabacloud-go/cas-20200407/v3/client"
	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	"github.com/alibabacloud-go/tea/tea"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// SSLClient 阿里云 CAS 客户端
type SSLClient struct {
	client *cas.Client         //阿里云 CAS 客户端
	cfg    config.AliyunConfig //该客户端对应的阿里云配置
	name   string              //配置名称（用于日志）
}

// CertificateExists 检查阿里云 SSL 中是否已存在指定名称证书
func (c *SSLClient) CertificateExists(name string) (bool, error) {
	certID, err := c.findCertificateByName(name)
	if err != nil {
		return false, err
	}
	return certID != "", nil
}

// InitialSyncMissingCertificates 启动时先全量比对，不存在则先同步到ssl
func (c *SSLClient) InitialSyncMissingCertificates(monitor *kubernetes.SecretMonitor, secrets []config.SecretConfig) error {
	log.Printf("[%s] Starting initial SSL compare for %d configured secret(s)", c.name, len(secrets))

	for _, secretCfg := range secrets {
		namespace := strings.TrimSpace(secretCfg.Namespace)
		name := strings.TrimSpace(secretCfg.Name)
		aliSSLName := strings.TrimSpace(secretCfg.AliSSLName)

		if namespace == "" {
			namespace = "default"
		}
		if name == "" {
			log.Printf("Skip initial compare: empty secret name in config (namespace=%s)", namespace)
			continue
		}
		if aliSSLName == "" {
			log.Printf("[%s] Skip initial compare: %s/%s has empty ali_ssl_name", c.name, namespace, name)
			continue
		}

		exists, err := c.CertificateExists(aliSSLName)
		if err != nil {
			return fmt.Errorf("[%s] compare aliyun SSL %q for secret %s/%s failed: %w", c.name, aliSSLName, namespace, name, err)
		}
		if exists {
			log.Printf("[%s] Aliyun SSL %q already exists, skip initial sync for %s/%s", c.name, aliSSLName, namespace, name)
			continue
		}

		secret, err := monitor.GetSecret(namespace, name)
		if err != nil {
			if apierrors.IsNotFound(err) {
				log.Printf("[%s] Secret %s/%s not found during initial sync, skip now and wait for watch events", c.name, namespace, name)
				continue
			}
			return fmt.Errorf("[%s] get secret %s/%s for initial sync failed: %w", c.name, namespace, name, err)
		}
		if err := c.SyncSecretToSSL(secret, aliSSLName); err != nil {
			return fmt.Errorf("[%s] initial sync secret %s/%s to aliyun SSL %q failed: %w", c.name, namespace, name, aliSSLName, err)
		}
		log.Printf("[%s] Initial sync succeeded for %s/%s -> Aliyun SSL %q", c.name, namespace, name, aliSSLName)
	}

	log.Printf("[%s] Initial SSL compare completed", c.name)
	return nil
}

// NewSSLClient 创建阿里云 CAS 客户端
func NewSSLClient(name string, cfg config.AliyunConfig) (*SSLClient, error) {
	config := &openapi.Config{
		//设置阿里云凭证
		AccessKeyId:     tea.String(cfg.AccessKeyID),
		AccessKeySecret: tea.String(cfg.AccessKeySecret),
	}

	endpoint := cfg.SSLEndpoint // 默认使用外网 endpoint
	if cfg.UseInternalEndpoint {
		endpoint = cfg.SSLInternalEndpoint
		if endpoint == "" {
			endpoint = "cas-vpc." + cfg.Region + ".aliyuncs.com"
		}
	}
	if endpoint == "" {
		endpoint = "cas.aliyuncs.com"
	}
	config.Endpoint = tea.String(endpoint)
	log.Printf("Using CAS endpoint: %s", endpoint)

	client, err := cas.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create aliyun CAS client: %v", err)
	}

	return &SSLClient{
		client: client,
		cfg:    cfg,
		name:   name,
	}, nil
}

// SyncSecretToSSL 同步 Secret 到阿里云 SSL
func (c *SSLClient) SyncSecretToSSL(secret *corev1.Secret, aliSSLName string) error {
	// 从secret中获取证书和私钥
	certData := secret.Data["tls.crt"]
	keyData := secret.Data["tls.key"]

	// 检查证书和私钥是否存在
	if len(certData) == 0 || len(keyData) == 0 {
		return fmt.Errorf("secret %s/%s does not contain tls.crt or tls.key",
			secret.Namespace, secret.Name)
	}

	certPEM := string(certData)
	keyPEM := string(keyData)

	// 解析证书和私钥
	cert, err := tls.X509KeyPair(certData, keyData)
	if err != nil {
		return fmt.Errorf("failed to parse certificate: %v", err)
	}

	// 检查证书是否为空
	if len(cert.Certificate) == 0 {
		return fmt.Errorf("certificate is empty")
	}

	log.Printf("Syncing certificate %s to Aliyun SSL...", aliSSLName)

	// 在阿里云SSL中查找证书
	certId, err := c.findCertificateByName(aliSSLName)
	if err != nil {
		return fmt.Errorf("failed to find certificate: %v", err)
	}

	// 如果证书存在，则删除阿里云SSL中的证书
	if certId != "" {
		log.Printf("Deleting existing certificate %s (ID: %s)", aliSSLName, certId)
		//删除阿里云SSL中的证书，如果证书不存在或者证书正在使用中，则删除失败
		if err := c.deleteCertificate(aliSSLName, certId); err != nil {
			return fmt.Errorf("failed to delete existing certificate: %v", err)
		}
	}

	log.Printf("Uploading new certificate %s", aliSSLName)
	// 上传证书到阿里云SSL
	return c.uploadCertificate(aliSSLName, certPEM, keyPEM)
}

// findCertificateByName 在阿里云SSL查找证书
func (c *SSLClient) findCertificateByName(name string) (string, error) {
	// UploadUserCertificate 的重名校验基于证书名称 Name，
	// 因此这里改为按 Name 查询并返回可删除用的 CertificateId。
	const pageSize int64 = 100
	var currentPage int64 = 1
	for {
		request := &cas.ListUserCertificateOrderRequest{
			OrderType:   tea.String("UPLOAD"),
			Keyword:     tea.String(name),
			CurrentPage: tea.Int64(currentPage),
			ShowSize:    tea.Int64(pageSize),
		}

		response, err := c.client.ListUserCertificateOrder(request)
		if err != nil {
			return "", fmt.Errorf("failed to list user certificates: %v", err)
		}
		if response == nil || response.Body == nil || len(response.Body.CertificateOrderList) == 0 {
			return "", nil
		}

		for _, cert := range response.Body.CertificateOrderList {
			if cert == nil {
				continue
			}
			log.Printf("ListUserCertificateOrder candidate: name=%q certificate_id=%d", tea.StringValue(cert.Name), tea.Int64Value(cert.CertificateId))
			if tea.StringValue(cert.Name) == name && cert.CertificateId != nil {
				log.Printf("ListUserCertificateOrder matched certificate: name=%q certificate_id=%d", tea.StringValue(cert.Name), tea.Int64Value(cert.CertificateId))
				return strconv.FormatInt(tea.Int64Value(cert.CertificateId), 10), nil
			}
		}

		if len(response.Body.CertificateOrderList) < int(pageSize) {
			break
		}
		currentPage++
	}

	return "", nil
}

// uploadCertificate 上传证书
func (c *SSLClient) uploadCertificate(name, certPEM, keyPEM string) error {
	// 创建请求
	request := &cas.UploadUserCertificateRequest{
		Name: tea.String(name),
		Cert: tea.String(certPEM),
		Key:  tea.String(keyPEM),
	}

	// 发送请求，上传证书到阿里云SSL
	_, err := c.client.UploadUserCertificate(request)
	if err != nil {
		return fmt.Errorf("failed to upload certificate: %v", err)
	}

	log.Printf("Certificate %s uploaded successfully", name)
	return nil
}

// deleteCertificate 删除阿里云SSL中的证书
func (c *SSLClient) deleteCertificate(certName, certId string) error {
	certIdInt, err := strconv.ParseInt(certId, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid certificate ID: %v", err)
	}

	request := &cas.DeleteUserCertificateRequest{
		CertId: tea.Int64(certIdInt),
	}

	_, err = c.client.DeleteUserCertificate(request)
	if err != nil {
		return fmt.Errorf("failed to delete certificate: %v", err)
	}

	log.Printf("Certificate deleted successfully: name=%q certificate_id=%s", certName, certId)
	return nil
}
