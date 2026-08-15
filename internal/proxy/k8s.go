package proxy

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/divergedev/diverge/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlcache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var k8sLogger = ctrl.Log.WithName("proxy").WithName("k8s")

// ErrCacheNotSynced is returned when the informer cache has not yet completed
// its initial sync with the Kubernetes API server.
var ErrCacheNotSynced = fmt.Errorf("cache not synced")

type ttlEntry struct {
	env       EnvironmentInfo
	expiresAt time.Time
}

// K8sEnvironmentLister resolves preview environments by watching Environment
// custom resources. It uses an informer cache for primary lookups, with an
// optional TTL-based fallback for direct API calls.
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

// NewK8sEnvironmentLister creates a Kubernetes-backed EnvironmentLister that
// watches Environment custom resources via an informer cache. If informer setup
// fails, it falls back to direct API calls with a short TTL cache.
func NewK8sEnvironmentLister(ctx context.Context, kubeconfig, namespace string, scheme *runtime.Scheme) (*K8sEnvironmentLister, error) {
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

	lister.startPruner(ctx)

	cacheOpts := ctrlcache.Options{
		Scheme: scheme,
		DefaultNamespaces: map[string]ctrlcache.Config{
			namespace: {},
		},
	}
	k8sCache, err := ctrlcache.New(config, cacheOpts)
	if err != nil {
		k8sLogger.V(0).Info("Failed to create informer cache, falling back to direct API", "error", err)
		lister.useFallback = true
		return lister, nil
	}

	informer, err := k8sCache.GetInformer(ctx, &v1alpha1.Environment{})
	if err != nil {
		k8sLogger.V(0).Info("Failed to get informer, falling back to direct API", "error", err)
		lister.useFallback = true
		return lister, nil
	}

	if _, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
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
			if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
				obj = d.Obj
			}
			if env, ok := obj.(*v1alpha1.Environment); ok {
				lister.mu.Lock()
				delete(lister.envCache, env.Name)
				lister.mu.Unlock()
			}
		},
	}); err != nil {
		k8sLogger.V(0).Info("Failed to add event handler, falling back to direct API", "error", err)
		lister.useFallback = true
		return lister, nil
	}

	lister.hasSynced = informer.HasSynced

	go func() {
		if err := k8sCache.Start(ctx); err != nil {
			k8sLogger.V(0).Info("Informer cache stopped", "error", err)
		}
	}()

	return lister, nil
}

func (l *K8sEnvironmentLister) startPruner(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				l.ttlMu.Lock()
				now := time.Now()
				for k, v := range l.ttlCache {
					if now.After(v.expiresAt) {
						delete(l.ttlCache, k)
					}
				}
				l.ttlMu.Unlock()
			}
		}
	}()
}

// HasSynced reports whether the informer cache has completed its initial list.
// In fallback mode (no informer), it always returns true.
func (l *K8sEnvironmentLister) HasSynced() bool {
	if l.hasSynced == nil {
		return true // Fallback mode
	}
	return l.hasSynced()
}

// GetEnvironment looks up a single environment by name. It returns the cached
// entry if the informer is synced, falls back to a TTL-cached API call if in
// fallback mode, or returns ErrCacheNotSynced if the cache is not ready.
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
		return nil, ErrEnvironmentNotFound
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

// ListEnvironments returns all known environments. In informer mode it reads
// from the cache; in fallback mode it issues a List call to the Kubernetes API.
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
