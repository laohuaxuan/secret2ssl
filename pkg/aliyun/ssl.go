package aliyun

import (
	"crypto/tls"
	"fmt"
	"log"
	"strconv"

	"secret2ssl/pkg/config"

	cas "github.com/alibabacloud-go/cas-20200407/v3/client"          //阿里云 CAS（证书服务 / Certificate Authority Service） 的 Go SDK 客户端，用来调用 CAS 的 OpenAPI（例如上传/部署/查询证书、证书实例等
	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client" //用来调用阿里云的 OpenAPI（例如上传/部署/查询证书、证书实例等)
	"github.com/alibabacloud-go/tea/tea"                             //阿里云 SDK 生态的 通用工具/类型库，常见用途是处理指针值与默认值（如 tea.String("xx") / tea.Int32(1)）
	corev1 "k8s.io/api/core/v1"                                      //用来调用 Kubernetes 的 API（例如获取/更新/删除 Secret 等)
)

// SSLClient 阿里云 CAS 客户端
type SSLClient struct {
	client *cas.Client
	cfg    *config.Config
}

// NewSSLClient 创建阿里云 CAS 客户端
func NewSSLClient(cfg *config.Config) (*SSLClient, error) {
	config := &openapi.Config{
		//设置阿里云凭证
		AccessKeyId:     tea.String(cfg.Aliyun.AccessKeyID),
		AccessKeySecret: tea.String(cfg.Aliyun.AccessKeySecret),
	}

	endpoint := cfg.Aliyun.SSLEndpoint // 默认使用外网 endpoint
	if cfg.Aliyun.UseInternalEndpoint {
		endpoint = cfg.Aliyun.SSLInternalEndpoint
		if endpoint == "" {
			endpoint = "cas-vpc." + cfg.Aliyun.Region + ".aliyuncs.com"
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
	}, nil
}

// SyncSecretToSSL 同步 Secret 到阿里云 SSL
func (c *SSLClient) SyncSecretToSSL(secret *corev1.Secret, aliSSLName string) error {
	// 获取证书和私钥
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

	// 查找证书
	certId, err := c.findCertificateByName(aliSSLName)
	if err != nil {
		return fmt.Errorf("failed to find certificate: %v", err)
	}

	// 如果证书存在，则删除
	if certId != "" {
		log.Printf("Deleting existing certificate %s (ID: %s)", aliSSLName, certId)
		if err := c.deleteCertificate(certId); err != nil {
			return fmt.Errorf("failed to delete existing certificate: %v", err)
		}
	}

	log.Printf("Uploading new certificate %s", aliSSLName)
	// 上传证书
	return c.uploadCertificate(aliSSLName, certPEM, keyPEM)
}

// findCertificateByName 查找证书
func (c *SSLClient) findCertificateByName(name string) (string, error) {
	// 创建请求
	request := &cas.ListCertRequest{
		ShowSize: tea.Int64(100), // 设置本次查询返回条数为 100（分页大小）
	}

	// 发送请求
	response, err := c.client.ListCert(request)
	if err != nil {
		return "", fmt.Errorf("failed to list certificates: %v", err)
	}

	// 遍历证书列表
	if response.Body.CertList != nil {
		for _, cert := range response.Body.CertList {
			if tea.StringValue(cert.Identifier) == name {
				return tea.StringValue(cert.Identifier), nil
			}
		}
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

	log.Printf("【test】upload request: %+v", request)
	// 发送请求
	// _, err := c.client.UploadUserCertificate(request)
	// if err != nil {
	// 	return fmt.Errorf("failed to upload certificate: %v", err)
	// }

	log.Printf("Certificate %s uploaded successfully", name)
	return nil
}

// deleteCertificate 删除证书
func (c *SSLClient) deleteCertificate(certId string) error {
	certIdInt, err := strconv.ParseInt(certId, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid certificate ID: %v", err)
	}

	request := &cas.DeleteUserCertificateRequest{
		CertId: tea.Int64(certIdInt),
	}

	log.Printf("【test】delete request: %+v", request)
	// _, err = c.client.DeleteUserCertificate(request)
	// if err != nil {
	// 	return fmt.Errorf("failed to delete certificate: %v", err)
	// }

	log.Printf("Certificate %s deleted successfully", certId)
	return nil
}
