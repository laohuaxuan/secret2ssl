package kubernetes

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"secret2ssl/pkg/config"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// SecretChangeCallback 回调函数,当监听到 Secret 变化（新增/更新）时，调用这个函数执行业务逻辑（例如解析证书并同步到阿里云）
type SecretChangeCallback func(secret *corev1.Secret)

// SecretMonitor 密钥监控器
type SecretMonitor struct {
	clientset      *kubernetes.Clientset         //Kubernetes API 客户端句柄
	cfg            *config.Config                //配置
	callback       SecretChangeCallback          //事件回调函数
	watchedSecrets map[string]bool               //已监听的 Secret 列表,用于记录哪些 Secret 正在被监听
	watchCancels   map[string]context.CancelFunc //用于取消监听的上下文
	mu             sync.RWMutex                  //互斥锁，用于保护共享资源
}

// NewClientset 创建 Kubernetes API 客户端句柄
func NewClientset(cfg *config.Config) (*kubernetes.Clientset, error) {
	var kubeConfig *rest.Config
	var err error

	if cfg.Kubernetes.InCluster {
		// in-cluster 模式初始化 Kubernetes 配置（serviceAccount）
		kubeConfig, err = rest.InClusterConfig()
	} else if cfg.Kubernetes.Kubeconfig != "" {
		// kubeconfig 模式初始化 Kubernetes 配置（本地开发使用）
		kubeConfig, err = clientcmd.BuildConfigFromFlags("", cfg.Kubernetes.Kubeconfig)
	} else {
		return nil, fmt.Errorf("must specify either in_cluster or kubeconfig in kubernetes config")
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes clientset: %v", err)
	}
	return clientset, nil
}

// NewSecretMonitor 创建密钥监控器
func NewSecretMonitor(cfg *config.Config, callback SecretChangeCallback) (*SecretMonitor, error) {
	//创建 Kubernetes API 客户端句柄
	clientset, err := NewClientset(cfg)
	if err != nil {
		return nil, err
	}

	return &SecretMonitor{
		clientset:      clientset,
		cfg:            cfg,
		callback:       callback,
		watchedSecrets: make(map[string]bool),
		watchCancels:   make(map[string]context.CancelFunc),
	}, nil
}

// Start 启动密钥监控器
func (m *SecretMonitor) Start() error {
	log.Println("Starting secret monitor...")

	//遍历需要监听的 Secret 列表
	for _, secretCfg := range m.getSecrets() {
		//启动一个 goroutine 监听 Secret 变化
		m.addWatchedSecret(secretCfg.Namespace, secretCfg.Name)
		time.Sleep(100 * time.Millisecond) //等待 100 毫秒，避免短时间内启动太多 goroutine
	}

	log.Printf("Watching %d secrets", m.watchedCount())
	return nil
}

// 监听 Secret 变化
func (m *SecretMonitor) watchSecret(ctx context.Context, namespace, name string) {
	log.Printf("Watching secret: %s/%s", namespace, name)

	for {
		//循环每轮开头先检查一次取消信号，确保 goroutine 能尽快响应 cancel()，避免“已经不需要监听了还继续跑”
		select {
		//如果这个 watcher 对应的 context 已被取消（比如配置热更新把这个 secret 从监听列表移除），就打印日志并 return，结束当前 goroutine。
		case <-ctx.Done():
			log.Printf("Stopped watching secret: %s/%s", namespace, name)
			return
		//如果还没取消，就不阻塞，立刻继续执行后续逻辑（创建/重连 watch）
		default:
		}

		//创建 Secret 监听器，用字段选择器把监听范围缩小到单个 Secret 名称，避免收到该命名空间所有 Secret 的事件
		//Watch()是k8s提供的实时事件流机制，会持续推送该资源的变化事件，事件类型通常包括：Added / Modified / Deleted
		watcher, err := m.clientset.CoreV1().Secrets(namespace).Watch(
			context.Background(),
			metav1.ListOptions{
				FieldSelector: fmt.Sprintf("metadata.name=%s", name),
			},
		)
		//如果创建监听器失败，则等待一段时间后重试
		if err != nil {
			log.Printf("Failed to create watcher for %s/%s: %v", namespace, name, err)
			if !m.waitOrDone(ctx, time.Duration(m.getResyncPeriod())*time.Second) {
				return
			}
			continue
		}

		//遍历watcher监听器返回的事件，负责“收事件、处理事件、断线重连、优雅退出”
		for {
			select {
			//收到取消信号（比如配置热更新移除该 secret），就停止 watcher 并打印日志，结束当前 goroutine。
			case <-ctx.Done():
				watcher.Stop() //停止 watcher
				log.Printf("Stopped watching secret: %s/%s", namespace, name)
				return //结束当前 goroutine
			case event, ok := <-watcher.ResultChan(): //从 watcher 的 ResultChan() 接收事件
				//如果 ResultChan() 关闭了（比如 watcher 断线重连失败），就goto reconnect 跳转到重连逻辑。
				if !ok {
					log.Printf("Watcher closed for %s/%s, reconnecting...", namespace, name)
					//等待一段时间后重试，如果等待时间到了还没收到新事件，就结束当前 goroutine。
					if !m.waitOrDone(ctx, time.Duration(m.getResyncPeriod())*time.Second) {
						return
					}
					goto reconnect //跳转到重连逻辑(创建新的watcher)
				}

				//将事件对象转换为 Secret 对象，后续业务要用 Secret 的字段
				secret, ok := event.Object.(*corev1.Secret)
				if !ok {
					log.Printf("Unexpected object type for %s/%s", namespace, name)
					continue
				}
				//如果事件类型为新增或更新，则调用回调函数执行业务逻辑
				if event.Type == watch.Added || event.Type == watch.Modified {
					log.Printf("Secret changed: %s/%s", namespace, name)
					if m.callback != nil {
						m.callback(secret)
					}
				} else if event.Type == watch.Deleted {
					log.Printf("Secret deleted: %s/%s", namespace, name)
				}
			}
		}
	reconnect:
	}
}

// GetSecret 获取 Secret
func (m *SecretMonitor) GetSecret(namespace, name string) (*corev1.Secret, error) {
	//获取 Secret
	return m.clientset.CoreV1().Secrets(namespace).Get(
		context.Background(),
		name,
		metav1.GetOptions{},
	)
}

// 添加需要监听的 Secret
func (m *SecretMonitor) addWatchedSecret(namespace, name string) {
	// namespace/name 的唯一标识
	key := secretKey(namespace, name)

	m.mu.Lock()
	defer m.mu.Unlock()
	//如果已经监听，则返回
	if m.watchedSecrets[key] {
		return
	}
	//创建一个上下文，用于取消监听
	ctx, cancel := context.WithCancel(context.Background())
	//将 Secret 添加到已监听的列表中
	m.watchedSecrets[key] = true
	//将取消监听的上下文添加到已监听的列表中
	m.watchCancels[key] = cancel
	//启动一个 goroutine 监听 Secret 变化
	go m.watchSecret(ctx, namespace, name)
}

// 获取已监听的 Secret 数量
func (m *SecretMonitor) watchedCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.watchedSecrets)
}

// 获取重试/重连等待时间
func (m *SecretMonitor) getResyncPeriod() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.Kubernetes.ResyncPeriod
}

// 获取需要监听的 Secret 列表
func (m *SecretMonitor) getSecrets() []config.SecretConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := m.cfg.AllSecrets()
	out := make([]config.SecretConfig, len(items))
	copy(out, items)
	return out
}

// 等待或完成
func (m *SecretMonitor) waitOrDone(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select { //如果 context 已取消，返回 false
	case <-ctx.Done():
		return false
	case <-timer.C: //如果等待时间到了，返回 true
		return true
	}
}

// 生成 Secret 的唯一标识
func secretKey(namespace, name string) string {
	return fmt.Sprintf("%s/%s", namespace, name)
}
