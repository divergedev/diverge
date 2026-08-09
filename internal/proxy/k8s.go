package proxy

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/divergedev/diverge/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type K8sEnvironmentLister struct {
	client    client.Client
	namespace string
	mu        sync.RWMutex
	cache     map[string]EnvironmentInfo
}

func NewK8sEnvironmentLister(kubeconfig, namespace string, scheme *runtime.Scheme) (*K8sEnvironmentLister, error) {
	var config *rest.Config
	var err error

	if kubeconfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		config, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, err
	}

	c, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		return nil, err
	}

	lister := &K8sEnvironmentLister{
		client:    c,
		namespace: namespace,
		cache:     make(map[string]EnvironmentInfo),
	}

	go lister.watch(context.Background())
	return lister, nil
}

func (l *K8sEnvironmentLister) watch(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var envList v1alpha1.EnvironmentList
			err := l.client.List(ctx, &envList, client.InNamespace(l.namespace))
			if err != nil {
				continue
			}
			newCache := make(map[string]EnvironmentInfo)
			for _, env := range envList.Items {
				newCache[env.Name] = EnvironmentInfo{
					Name:  env.Name,
					Phase: string(env.Status.Phase),
				}
			}
			l.mu.Lock()
			l.cache = newCache
			l.mu.Unlock()
		}
	}
}

func (l *K8sEnvironmentLister) GetEnvironment(ctx context.Context, name string) (*EnvironmentInfo, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	env, ok := l.cache[name]
	if !ok {
		return nil, fmt.Errorf("environment not found")
	}
	return &env, nil
}

func (l *K8sEnvironmentLister) ListEnvironments(ctx context.Context) ([]EnvironmentInfo, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var envs []EnvironmentInfo
	for _, env := range l.cache {
		envs = append(envs, env)
	}
	return envs, nil
}
