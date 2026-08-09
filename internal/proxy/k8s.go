package proxy

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/divergedev/diverge/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	ctrlcache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var ErrCacheNotSynced = fmt.Errorf("cache not synced")

type ttlEntry struct {
	env       EnvironmentInfo
	expiresAt time.Time
}

type K8sEnvironmentLister struct {
	client    client.Client
	namespace string

	mu       sync.RWMutex
	envCache map[string]EnvironmentInfo

	hasSynced   func() bool
	useFallback bool

	ttlMu    sync.RWMutex
	ttlCache map[string]*ttlEntry
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
		envCache:  make(map[string]EnvironmentInfo),
		ttlCache:  make(map[string]*ttlEntry),
	}

	cacheOpts := ctrlcache.Options{
		Scheme: scheme,
		DefaultNamespaces: map[string]ctrlcache.Config{
			namespace: {},
		},
	}
	k8sCache, err := ctrlcache.New(config, cacheOpts)
	if err != nil {
		log.Printf("Warning: Failed to create informer cache, falling back to direct API: %v", err)
		lister.useFallback = true
		return lister, nil
	}

	ctx := context.Background()
	informer, err := k8sCache.GetInformer(ctx, &v1alpha1.Environment{})
	if err != nil {
		log.Printf("Warning: Failed to get informer, falling back to direct API: %v", err)
		lister.useFallback = true
		return lister, nil
	}

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if env, ok := obj.(*v1alpha1.Environment); ok {
				lister.mu.Lock()
				lister.envCache[env.Name] = EnvironmentInfo{
					Name:  env.Name,
					Phase: string(env.Status.Phase),
				}
				lister.mu.Unlock()
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			if env, ok := newObj.(*v1alpha1.Environment); ok {
				lister.mu.Lock()
				lister.envCache[env.Name] = EnvironmentInfo{
					Name:  env.Name,
					Phase: string(env.Status.Phase),
				}
				lister.mu.Unlock()
			}
		},
		DeleteFunc: func(obj interface{}) {
			if env, ok := obj.(*v1alpha1.Environment); ok {
				lister.mu.Lock()
				delete(lister.envCache, env.Name)
				lister.mu.Unlock()
			}
		},
	})

	lister.hasSynced = informer.HasSynced

	go func() {
		if err := k8sCache.Start(ctx); err != nil {
			log.Printf("Informer cache stopped: %v", err)
		}
	}()

	return lister, nil
}

func (l *K8sEnvironmentLister) GetEnvironment(ctx context.Context, name string) (*EnvironmentInfo, error) {
	if l.useFallback {
		return l.getFallback(ctx, name)
	}

	if !l.hasSynced() {
		return nil, ErrCacheNotSynced
	}

	l.mu.RLock()
	defer l.mu.RUnlock()
	env, ok := l.envCache[name]
	if !ok {
		return nil, fmt.Errorf("environment not found")
	}
	return &env, nil
}

func (l *K8sEnvironmentLister) getFallback(ctx context.Context, name string) (*EnvironmentInfo, error) {
	l.ttlMu.RLock()
	entry, ok := l.ttlCache[name]
	l.ttlMu.RUnlock()

	if ok && time.Now().Before(entry.expiresAt) {
		return &entry.env, nil
	}

	var env v1alpha1.Environment
	err := l.client.Get(ctx, client.ObjectKey{Name: name, Namespace: l.namespace}, &env)
	if err != nil {
		return nil, err
	}

	info := EnvironmentInfo{
		Name:  env.Name,
		Phase: string(env.Status.Phase),
	}

	l.ttlMu.Lock()
	l.ttlCache[name] = &ttlEntry{
		env:       info,
		expiresAt: time.Now().Add(5 * time.Second),
	}
	l.ttlMu.Unlock()

	return &info, nil
}

func (l *K8sEnvironmentLister) ListEnvironments(ctx context.Context) ([]EnvironmentInfo, error) {
	if l.useFallback {
		var envList v1alpha1.EnvironmentList
		err := l.client.List(ctx, &envList, client.InNamespace(l.namespace))
		if err != nil {
			return nil, err
		}
		var envs []EnvironmentInfo
		for _, env := range envList.Items {
			envs = append(envs, EnvironmentInfo{
				Name:  env.Name,
				Phase: string(env.Status.Phase),
			})
		}
		return envs, nil
	}

	if !l.hasSynced() {
		return nil, ErrCacheNotSynced
	}

	l.mu.RLock()
	defer l.mu.RUnlock()
	var envs []EnvironmentInfo
	for _, env := range l.envCache {
		envs = append(envs, env)
	}
	return envs, nil
}
