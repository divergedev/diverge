package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/server/auth"
	"github.com/divergedev/diverge/internal/server/streaming"
)

const (
	// maxChunkBufferSize is the max memory to buffer for a single legacy v2 chunked response.
	maxChunkBufferSize = 10 << 20 // 10MB max per response
	// maxProxyRequestBodySize bounds inbound HTTP request bodies on :8081 for Protocol v3.
	maxProxyRequestBodySize = 10 << 20 // 10 MiB
	// maxConcurrentRequestsPerTunnel caps concurrent in-flight HTTP requests per v3 tunnel.
	maxConcurrentRequestsPerTunnel = 128
	// tunnelHeartbeatInterval is how often the server renews Leases (and pings v2 clients).
	tunnelHeartbeatInterval = 15 * time.Second
	// tunnelHeartbeatTimeout is how long v2 waits for a pong before closing the tunnel.
	tunnelHeartbeatTimeout = 45 * time.Second
	// defaultSARRecheckInterval is how often active tunnels re-verify K8s RBAC SAR.
	defaultSARRecheckInterval = 5 * time.Minute
	// defaultMaxTunnelLifetime caps how long a single tunnel stream may stay open.
	defaultMaxTunnelLifetime = 8 * time.Hour
	// tunnelReqChBuffer is the channel buffer for pending legacy v2 requests.
	tunnelReqChBuffer = 10
	// TunnelProxyPort is the dedicated port for cluster-internal tunnel proxy traffic.
	TunnelProxyPort = 8081
)

// TunnelManagerConfig configures TunnelManager behavior and security policies.
type TunnelManagerConfig struct {
	CRDClient               client.Client
	K8sClient               kubernetes.Interface
	InformerMgr             *streaming.InformerManager
	Directory               TunnelDirectory
	Logger                  *slog.Logger
	AuditLogger             *AuditLogger
	StreamLimiter           *StreamLimiter
	AllowedNamespaces       []string
	DefaultNamespace        string
	CreatePerTunnelServices *bool
	ProxyAuthToken          string
	ProxyServiceAddr        string
	SharedPreviewOwners     []string
	SARRecheckInterval      time.Duration
	MaxTunnelLifetime       time.Duration
}

type TunnelManager struct {
	mu             sync.RWMutex
	tunnels        map[string]*activeTunnel
	admitted       map[string]context.CancelFunc
	evictedHolders map[string]struct{}
	crdClient      client.Client
	k8sClient      kubernetes.Interface
	informerMgr    *streaming.InformerManager
	directory      TunnelDirectory
	logger         *slog.Logger
	auditLogger    *AuditLogger
	streamLim      *StreamLimiter
	lease          *TunnelLease
	gc             *TunnelGC
	podName        string

	allowedNamespaces       map[string]bool
	defaultNamespace        string
	createPerTunnelServices bool
	proxyAuthToken          string
	proxyServiceAddr        string
	sharedPreviewOwners     map[string]bool
	sarRecheckInterval      time.Duration
	maxTunnelLifetime       time.Duration

	shuttingDown atomic.Bool
	activeWg     sync.WaitGroup
}

type chunkAssembly struct {
	statusCode int32
	headers    []*pb.HeaderEntry
	body       []byte
}

type activeTunnel struct {
	admitID          string
	tunnelID         string
	previewID        string
	service          string
	namespace        string
	port             int32
	protocolVersion  int32
	leaseName        string
	k8sSvcName       string
	owner            string
	clientInstanceID string
	metadata         map[string]string
	startedAt        time.Time
	expiresAt        time.Time

	// Protocol v3 reverse HTTP/2 state
	bidiConn    *bidiStreamConn
	http2CC     *http.ClientConn
	proxy       *httputil.ReverseProxy
	sem         chan struct{}
	reqMu       sync.RWMutex
	reqDraining bool
	reqWg       sync.WaitGroup

	// Legacy v2 buffered state
	reqCh       chan *pb.TunnelHTTPRequest
	respMu      sync.RWMutex
	respWg      map[string]chan *pb.TunnelHTTPResponse
	chunkMu     sync.Mutex
	chunkBuffer map[string]*chunkAssembly
	lastPong    time.Time
	lastPongMu  sync.RWMutex

	sendCloseFn func(pb.CloseCode, string)
	ctx         context.Context
	cancel      context.CancelFunc
}

func NewTunnelManager(crdClient client.Client, k8s kubernetes.Interface, logger *slog.Logger, audit *AuditLogger) *TunnelManager {
	return NewTunnelManagerWithConfig(TunnelManagerConfig{
		CRDClient:   crdClient,
		K8sClient:   k8s,
		Logger:      logger,
		AuditLogger: audit,
	})
}

func NewTunnelManagerWithConfig(cfg TunnelManagerConfig) *TunnelManager {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	podName := os.Getenv("POD_NAME")
	if podName == "" {
		podName = "diverge-server-local"
	}

	createServices := true
	if cfg.CreatePerTunnelServices != nil {
		createServices = *cfg.CreatePerTunnelServices
	}

	allowedNS := make(map[string]bool, len(cfg.AllowedNamespaces))
	for _, ns := range cfg.AllowedNamespaces {
		if trimmed := strings.TrimSpace(ns); trimmed != "" {
			allowedNS[trimmed] = true
		}
	}

	sharedOwners := make(map[string]bool, len(cfg.SharedPreviewOwners))
	for _, owner := range cfg.SharedPreviewOwners {
		if trimmed := strings.TrimSpace(owner); trimmed != "" {
			sharedOwners[trimmed] = true
		}
	}

	sarInterval := cfg.SARRecheckInterval
	if sarInterval <= 0 {
		sarInterval = defaultSARRecheckInterval
	}
	maxLifetime := cfg.MaxTunnelLifetime
	if maxLifetime <= 0 {
		maxLifetime = defaultMaxTunnelLifetime
	}

	tm := &TunnelManager{
		tunnels:                 make(map[string]*activeTunnel),
		admitted:                make(map[string]context.CancelFunc),
		evictedHolders:          make(map[string]struct{}),
		crdClient:               cfg.CRDClient,
		k8sClient:               cfg.K8sClient,
		informerMgr:             cfg.InformerMgr,
		logger:                  logger,
		auditLogger:             cfg.AuditLogger,
		streamLim:               cfg.StreamLimiter,
		podName:                 podName,
		lease:                   NewTunnelLease(cfg.K8sClient, logger, podName),
		gc:                      NewTunnelGC(cfg.K8sClient, logger).WithCreateServices(createServices),
		allowedNamespaces:       allowedNS,
		defaultNamespace:        strings.TrimSpace(cfg.DefaultNamespace),
		createPerTunnelServices: createServices,
		proxyAuthToken:          strings.TrimSpace(cfg.ProxyAuthToken),
		proxyServiceAddr:        strings.TrimSpace(cfg.ProxyServiceAddr),
		sharedPreviewOwners:     sharedOwners,
		sarRecheckInterval:      sarInterval,
		maxTunnelLifetime:       maxLifetime,
	}
	if cfg.Directory != nil {
		tm.directory = cfg.Directory
	} else {
		tm.directory = &localTunnelDirectory{tm: tm}
	}
	return tm
}

func (tm *TunnelManager) holderIdentity(tunnelID string) string {
	return fmt.Sprintf("%s:%s", tm.podName, tunnelID)
}

// ProxyAuthToken returns the configured shared secret for :8081 tunnel proxy requests.
func (tm *TunnelManager) ProxyAuthToken() string {
	return tm.proxyAuthToken
}

// IsReady returns false once graceful server shutdown has started.
func (tm *TunnelManager) IsReady() bool {
	return !tm.shuttingDown.Load()
}

// Shutdown notifies all connected tunnels with CLOSE_CODE_SERVER_SHUTDOWN, drains active
// Protocol v3 requests up to 5s, cancels tunnel contexts, and waits for Lease releases.
func (tm *TunnelManager) Shutdown(ctx context.Context) {
	tm.mu.Lock()
	if !tm.shuttingDown.CompareAndSwap(false, true) {
		tm.mu.Unlock()
		return
	}

	active := make([]*activeTunnel, 0, len(tm.tunnels))
	activeAdmitIDs := make(map[string]struct{}, len(tm.tunnels))
	for key, at := range tm.tunnels {
		if at != nil {
			active = append(active, at)
			if at.admitID != "" {
				activeAdmitIDs[at.admitID] = struct{}{}
			}
			decActiveTunnels(at.namespace)
		}
		delete(tm.tunnels, key)
	}
	var earlyCancels []context.CancelFunc
	allCancels := make([]context.CancelFunc, 0, len(tm.admitted))
	for id, cancelFn := range tm.admitted {
		allCancels = append(allCancels, cancelFn)
		if _, isActive := activeAdmitIDs[id]; !isActive {
			earlyCancels = append(earlyCancels, cancelFn)
		}
	}
	tm.mu.Unlock()

	for _, cancelFn := range earlyCancels {
		cancelFn()
	}

	if len(active) > 0 {
		tm.logger.Info("draining active tunnels for shutdown", "count", len(active))
		drainTimeout := 5 * time.Second
		if deadline, ok := ctx.Deadline(); ok {
			if rem := time.Until(deadline) / 2; rem > 0 && rem < drainTimeout {
				drainTimeout = rem
			}
		}
		drainCtx, drainCancel := context.WithTimeout(ctx, drainTimeout)
		var drainWg sync.WaitGroup
		for _, at := range active {
			if at.bidiConn != nil {
				at.bidiConn.BeginDrain()
			}
			at.reqMu.Lock()
			at.reqDraining = true
			at.reqMu.Unlock()

			drainWg.Add(1)
			go func(t *activeTunnel) {
				defer drainWg.Done()
				reqDone := make(chan struct{})
				go func() {
					t.reqWg.Wait()
					close(reqDone)
				}()
				select {
				case <-reqDone:
				case <-drainCtx.Done():
				}
			}(at)
		}
		drainWg.Wait()
		drainCancel()

		var closeWg sync.WaitGroup
		for _, at := range active {
			closeWg.Add(1)
			go func(t *activeTunnel) {
				defer closeWg.Done()
				if t.sendCloseFn != nil {
					sent := make(chan struct{})
					go func() {
						defer close(sent)
						t.sendCloseFn(pb.CloseCode_CLOSE_CODE_SERVER_SHUTDOWN, "server shutting down")
					}()
					timer := time.NewTimer(250 * time.Millisecond)
					select {
					case <-sent:
						timer.Stop()
					case <-timer.C:
					}
				}
				t.cancel()
			}(at)
		}
		closeWg.Wait()
	}

	for _, cancelFn := range allCancels {
		cancelFn()
	}

	done := make(chan struct{})
	go func() {
		tm.activeWg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
	}
}

// StartGC starts the background garbage collector for tunnel resources.
func (tm *TunnelManager) StartGC(ctx context.Context, namespaces []string) {
	go tm.gc.Run(ctx, namespaces)
}

func (tm *TunnelManager) isNamespaceAllowed(ns string) bool {
	if len(tm.allowedNamespaces) == 0 {
		return true
	}
	return tm.allowedNamespaces[ns]
}

func (tm *TunnelManager) effectiveDefaultNamespace() string {
	if tm.defaultNamespace != "" {
		return tm.defaultNamespace
	}
	return "default"
}

func (tm *TunnelManager) Tunnel(ctx context.Context, stream *connect.BidiStream[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse]) error {
	admitID := uuid.New().String()
	admitCtx, admitCancel := context.WithCancel(ctx)
	tm.mu.Lock()
	if tm.shuttingDown.Load() {
		tm.mu.Unlock()
		admitCancel()
		return connect.NewError(connect.CodeUnavailable, errors.New("server is shutting down"))
	}
	if tm.admitted != nil {
		tm.admitted[admitID] = admitCancel
	}
	tm.activeWg.Add(1)
	tm.mu.Unlock()
	defer func() {
		admitCancel()
		tm.mu.Lock()
		delete(tm.admitted, admitID)
		tm.mu.Unlock()
		tm.activeWg.Done()
	}()

	user, ok := auth.UserInfoFromContext(admitCtx)
	if !ok || user == nil {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("not authenticated"))
	}
	if user.FromCookie {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("bearer token required for tunnel stream"))
	}
	if isMacaroonUser(user) {
		return connect.NewError(connect.CodePermissionDenied, errors.New("capability tokens cannot establish developer tunnels"))
	}
	if !user.ExpiresAt.IsZero() && !user.ExpiresAt.After(time.Now()) {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("authentication token has expired"))
	}

	if tm.streamLim != nil {
		release, err := tm.streamLim.Acquire(admitCtx)
		if err != nil {
			return err
		}
		defer release()
	}

	// 1. Receive and validate TunnelRegister
	msg, err := stream.Receive()
	if err != nil {
		return fmt.Errorf("failed to receive register message: %w", err)
	}
	reg := msg.GetRegister()
	if reg == nil {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("first message must be TunnelRegister"))
	}

	namespace := strings.TrimSpace(reg.Namespace)
	if namespace == "" {
		namespace = tm.effectiveDefaultNamespace()
	}
	if err := ValidateDNS1123Label(reg.PreviewId, "preview_id"); err != nil {
		return err
	}
	if err := ValidateDNS1123Label(namespace, "namespace"); err != nil {
		return err
	}
	if reg.Service != "" {
		if err := ValidateDNS1123Label(reg.Service, "service"); err != nil {
			return err
		}
	}
	if !tm.isNamespaceAllowed(namespace) {
		return connect.NewError(connect.CodePermissionDenied, fmt.Errorf("namespace %q is not allowed for tunnels", namespace))
	}
	if reg.Port < 1 || reg.Port > 65535 {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("port must be between 1 and 65535, got %d", reg.Port))
	}
	metadataJSON, metaErr := encodeTunnelMetadata(reg.Metadata)
	if metaErr != nil {
		return connect.NewError(connect.CodeInvalidArgument, metaErr)
	}
	if err := validateClientInstanceID(reg.ClientInstanceId); err != nil {
		return err
	}

	effectiveReg := &pb.TunnelRegister{
		Service:          reg.Service,
		Namespace:        namespace,
		Port:             reg.Port,
		PreviewId:        reg.PreviewId,
		ProtocolVersion:  reg.ProtocolVersion,
		Metadata:         reg.Metadata,
		Force:            reg.Force,
		SkipK8SService:   reg.SkipK8SService,
		ClientInstanceId: reg.ClientInstanceId,
	}

	// 2. RBAC check
	if err := AuthorizeAction(admitCtx, tm.k8sClient, tm.auditLogger, "create", namespace, "environments"); err != nil {
		return err
	}

	// 3. Validate preview-id ownership (with cluster-scoped PreviewGroup + CI shared-owner support)
	if err := tm.validatePreviewOwnership(admitCtx, effectiveReg); err != nil {
		var cErr *connect.Error
		if errors.As(err, &cErr) {
			return cErr
		}
		return connect.NewError(connect.CodePermissionDenied, err)
	}

	protoVer := int32(2)
	if effectiveReg.ProtocolVersion >= 3 {
		protoVer = 3
	}
	createSvc := tm.createPerTunnelServices && (protoVer < 3 || !effectiveReg.SkipK8SService)

	leaseService := effectiveReg.Service
	if protoVer < 3 {
		leaseService = ""
	}
	leaseName := tunnelLeaseName(effectiveReg.PreviewId, leaseService)
	var k8sSvcName string
	if createSvc {
		k8sSvcName = tunnelServiceName(effectiveReg.PreviewId, leaseService)
	}

	podIP := os.Getenv("POD_IP")
	if podIP == "" {
		podIP = "127.0.0.1"
	}

	tunnelID := uuid.New().String()
	holderID := tm.holderIdentity(tunnelID)
	defer func() {
		tm.mu.Lock()
		delete(tm.evictedHolders, holderID)
		tm.mu.Unlock()
	}()
	protoTag := fmt.Sprintf("v%d", protoVer)

	// 4. Acquire atomic Lease with client_instance_id fencing & RBAC-gated cross-user --force
	leaseRes, err := tm.lease.Acquire(admitCtx, LeaseAcquireOptions{
		Namespace:        namespace,
		PreviewID:        effectiveReg.PreviewId,
		Service:          leaseService,
		LeaseName:        leaseName,
		TunnelID:         tunnelID,
		ClientInstanceID: effectiveReg.ClientInstanceId,
		Owner:            user.Username,
		K8sServiceName:   k8sSvcName,
		PodIP:            podIP,
		Port:             effectiveReg.Port,
		Protocol:         protoTag,
		MetadataJSON:     metadataJSON,
		Force:            effectiveReg.Force,
		CheckCrossUserForce: func(_ string) error {
			if err := AuthorizeAction(admitCtx, tm.k8sClient, tm.auditLogger, "update", namespace, "environments"); err != nil {
				return err
			}
			if err := AuthorizeAction(admitCtx, tm.k8sClient, tm.auditLogger, "delete", namespace, "environments"); err != nil {
				return err
			}
			return nil
		},
	})
	if err != nil {
		var cErr *connect.Error
		if errors.As(err, &cErr) {
			return cErr
		}
		if errors.Is(err, ErrTunnelConflict) {
			return connect.NewError(connect.CodeAlreadyExists, err)
		}
		return fmt.Errorf("failed to acquire tunnel lease: %w", err)
	}

	if leaseRes.Evicted {
		if effectiveReg.Force && tm.auditLogger != nil {
			if leaseRes.PreviousOwner == user.Username {
				tm.auditLogger.LogMutation(admitCtx, "tunnel.evict_self_force", "tunnel", leaseName, namespace)
			} else {
				tm.auditLogger.LogMutation(admitCtx, "tunnel.evict_cross_user_force", "tunnel", leaseName, namespace)
			}
		}
		tm.evictLocalTunnel(namespace, leaseName, leaseRes.PreviousHolder, "evicted by replacement tunnel")
	}

	tm.logger.Info("tunnel registering",
		"preview-id", effectiveReg.PreviewId,
		"namespace", namespace,
		"service", effectiveReg.Service,
		"port", effectiveReg.Port,
		"protocol", protoTag,
		"tunnel-id", tunnelID,
	)

	startedAt := time.Now()
	expiresAt := startedAt.Add(tm.maxTunnelLifetime)
	if !user.ExpiresAt.IsZero() && user.ExpiresAt.Before(expiresAt) {
		expiresAt = user.ExpiresAt
	}
	tCtx, tCancel := context.WithDeadline(admitCtx, expiresAt)
	defer tCancel()

	// 5. Optionally create per-tunnel K8s Service + EndpointSlice + Endpoints owned by Lease
	if createSvc {
		if err := tm.createTunnelResources(tCtx, effectiveReg, k8sSvcName, podIP, tunnelID, leaseRes); err != nil {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
			tm.lease.Release(cleanupCtx, namespace, leaseName, tunnelID)
			cleanupCancel()
			return fmt.Errorf("failed to create tunnel resources: %w", err)
		}
	}

	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if tm.lease.Release(cleanupCtx, namespace, leaseName, tunnelID) && createSvc {
			tm.deleteTunnelResources(cleanupCtx, namespace, k8sSvcName, tunnelID)
		}
	}()

	// 6. Send TunnelReady BEFORE starting v3 HTTP/2 client preface so CLI always receives Ready first
	endpoint := fmt.Sprintf("%s:%d", podIP, TunnelProxyPort)
	ready := &pb.TunnelReady{
		TunnelId:        tunnelID,
		Endpoint:        endpoint,
		ProxyService:    tm.proxyServiceAddr,
		ProtocolVersion: protoVer,
		K8SServiceName:  k8sSvcName,
	}

	var metaCopy map[string]string
	if len(effectiveReg.Metadata) > 0 {
		metaCopy = make(map[string]string, len(effectiveReg.Metadata))
		for k, v := range effectiveReg.Metadata {
			metaCopy[k] = v
		}
	}

	at := &activeTunnel{
		admitID:          admitID,
		tunnelID:         tunnelID,
		previewID:        effectiveReg.PreviewId,
		service:          effectiveReg.Service,
		namespace:        namespace,
		port:             effectiveReg.Port,
		protocolVersion:  protoVer,
		leaseName:        leaseName,
		k8sSvcName:       k8sSvcName,
		owner:            user.Username,
		clientInstanceID: effectiveReg.ClientInstanceId,
		metadata:         metaCopy,
		startedAt:        startedAt,
		expiresAt:        expiresAt,
		lastPong:         startedAt,
		ctx:              tCtx,
		cancel:           tCancel,
	}

	if protoVer >= 3 {
		return tm.runV3Tunnel(tCtx, tCancel, stream, at, ready, user)
	}
	return tm.runV2Tunnel(tCtx, tCancel, stream, at, ready, user)
}

func validateClientInstanceID(id string) error {
	if id == "" {
		return nil
	}
	if len(id) > 128 {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("client_instance_id exceeds maximum length of 128 characters"))
	}
	for i := 0; i < len(id); i++ {
		if id[i] < 0x20 || id[i] > 0x7e {
			return connect.NewError(connect.CodeInvalidArgument, errors.New("client_instance_id must contain only printable ASCII characters"))
		}
	}
	return nil
}

func (tm *TunnelManager) registerActiveTunnel(at *activeTunnel) (string, bool) {
	mapSvc := at.service
	if at.protocolVersion < 3 {
		mapSvc = ""
	}
	mapKey := tunnelMapKey(at.namespace, at.previewID, mapSvc)
	holderID := tm.holderIdentity(at.tunnelID)

	tm.mu.Lock()
	defer tm.mu.Unlock()
	if tm.shuttingDown.Load() {
		return "", false
	}
	if _, evicted := tm.evictedHolders[holderID]; evicted {
		delete(tm.evictedHolders, holderID)
		return "", false
	}
	if tm.tunnels == nil {
		tm.tunnels = make(map[string]*activeTunnel)
	}
	if prev := tm.tunnels[mapKey]; prev != nil {
		decActiveTunnels(prev.namespace)
	}
	tm.tunnels[mapKey] = at
	incActiveTunnels(at.namespace)
	return mapKey, true
}

func (tm *TunnelManager) unregisterActiveTunnel(mapKey string, at *activeTunnel) {
	tm.mu.Lock()
	if tm.tunnels[mapKey] == at {
		delete(tm.tunnels, mapKey)
		decActiveTunnels(at.namespace)
	}
	tm.mu.Unlock()
}

func (tm *TunnelManager) startTunnelLifetimeAndMaintenance(
	tCtx context.Context,
	tCancel context.CancelFunc,
	at *activeTunnel,
	user *auth.UserInfo,
	onHeartbeatTick func() error,
) <-chan struct{} {
	maintDone := make(chan struct{})
	maxDur := tm.maxTunnelLifetime
	if !user.ExpiresAt.IsZero() {
		untilExp := time.Until(user.ExpiresAt)
		if untilExp <= 0 {
			at.sendCloseFn(pb.CloseCode_CLOSE_CODE_AUTH_EXPIRED, "authentication token expired")
			tCancel()
			close(maintDone)
			return maintDone
		}
		if untilExp < maxDur {
			maxDur = untilExp
		}
	}

	lifetimeTimer := time.NewTimer(maxDur)
	leaseTicker := time.NewTicker(tunnelHeartbeatInterval)
	sarTicker := time.NewTicker(tm.sarRecheckInterval)

	go func() {
		defer close(maintDone)
		defer lifetimeTimer.Stop()
		defer leaseTicker.Stop()
		defer sarTicker.Stop()
		defer func() {
			if r := recover(); r != nil {
				tm.logger.Error("panic in tunnel maintenance goroutine", "err", r)
			}
		}()

		consecutiveTransient := 0
		for {
			select {
			case <-tCtx.Done():
				return
			case <-lifetimeTimer.C:
				tm.logger.Info("tunnel lifetime/token expiry reached, closing", "preview-id", at.previewID, "service", at.service)
				at.sendCloseFn(pb.CloseCode_CLOSE_CODE_AUTH_EXPIRED, "tunnel session lifetime or token expired")
				tCancel()
				return
			case <-sarTicker.C:
				checkCtx := auth.ContextWithUserInfo(tCtx, user)
				if !user.ExpiresAt.IsZero() && !user.ExpiresAt.After(time.Now()) {
					at.sendCloseFn(pb.CloseCode_CLOSE_CODE_AUTH_EXPIRED, "authentication token expired")
					tCancel()
					return
				}
				if sarErr := AuthorizeAction(checkCtx, tm.k8sClient, tm.auditLogger, "create", at.namespace, "environments"); sarErr != nil && tCtx.Err() == nil {
					tm.logger.Warn("tunnel SAR re-check failed, closing", "preview-id", at.previewID, "user", at.owner, "err", sarErr)
					at.sendCloseFn(pb.CloseCode_CLOSE_CODE_AUTH_EXPIRED, "authorization revoked")
					tCancel()
					return
				}
			case <-leaseTicker.C:
				if onHeartbeatTick != nil {
					if hbErr := onHeartbeatTick(); hbErr != nil {
						tCancel()
						return
					}
				}
				renewState, renewErr := tm.lease.Renew(tCtx, at.namespace, at.leaseName, at.tunnelID)
				switch renewState {
				case RenewOK:
					consecutiveTransient = 0
				case RenewEvicted:
					tm.logger.Info("lease lost, closing tunnel", "preview-id", at.previewID, "service", at.service)
					at.sendCloseFn(pb.CloseCode_CLOSE_CODE_EVICTED, "tunnel lease lost or evicted")
					tCancel()
					return
				case RenewTransientError:
					consecutiveTransient++
					if consecutiveTransient >= 2 {
						tm.logger.Warn("lease renewal failed twice consecutively, closing tunnel", "preview-id", at.previewID, "err", renewErr)
						tCancel()
						return
					}
				}
			}
		}
	}()
	return maintDone
}

func (tm *TunnelManager) runV3Tunnel(
	tCtx context.Context,
	tCancel context.CancelFunc,
	stream *connect.BidiStream[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse],
	at *activeTunnel,
	ready *pb.TunnelReady,
	user *auth.UserInfo,
) error {
	if err := stream.Send(&pb.TunnelServiceTunnelResponse{
		Payload: &pb.TunnelServiceTunnelResponse_Ready{Ready: ready},
	}); err != nil {
		return err
	}

	bidiConn := newBidiStreamConn(&serverPacketStream{stream: stream})
	cc, err := newTunnelHTTP2ClientConn(tCtx, bidiConn, 15*time.Second, 30*time.Second)
	if err != nil {
		_ = bidiConn.Close()
		return fmt.Errorf("failed to initialize HTTP/2 client conn over tunnel: %w", err)
	}
	bidiConn.SetOnClose(tCancel)
	cc.SetStateHook(func(c *http.ClientConn) {
		if c.Err() != nil {
			tCancel()
		}
	})

	at.bidiConn = bidiConn
	at.http2CC = cc
	at.sem = make(chan struct{}, maxConcurrentRequestsPerTunnel)
	at.sendCloseFn = func(code pb.CloseCode, reason string) {
		_ = bidiConn.SendClose(code, reason)
	}
	at.proxy = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = "tunnel.local"
			stripDivergeHeadersAndTrailers(pr.Out.Header)
			pr.SetXForwarded()
		},
		Transport: &headerTimeoutRoundTripper{
			base:        cc,
			timeout:     30 * time.Second,
			idleTimeout: 5 * time.Minute,
		},
		FlushInterval: -1,
		ModifyResponse: func(resp *http.Response) error {
			if resp.StatusCode < 200 || resp.StatusCode > 599 {
				return fmt.Errorf("invalid upstream status code %d", resp.StatusCode)
			}
			stripDivergeHeadersAndTrailers(resp.Header)
			stripDivergeHeadersAndTrailers(resp.Trailer)
			if resp.Body != nil {
				resp.Body = &trailerStrippingBody{
					ReadCloser: resp.Body,
					trailer:    resp.Trailer,
				}
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			if errors.Is(err, errResponseHeaderTimeout) || errors.Is(err, errStreamIdleTimeout) {
				http.Error(w, "gateway timeout", http.StatusGatewayTimeout)
				return
			}
			tm.logger.Warn("tunnel proxy upstream error", "err", err, "preview_id", at.previewID, "service", at.service)
			http.Error(w, "bad gateway", http.StatusBadGateway)
		},
	}

	mapKey, ok := tm.registerActiveTunnel(at)
	if !ok {
		if tm.shuttingDown.Load() {
			at.sendCloseFn(pb.CloseCode_CLOSE_CODE_SERVER_SHUTDOWN, "server shutting down")
		} else {
			at.sendCloseFn(pb.CloseCode_CLOSE_CODE_EVICTED, "evicted by replacement tunnel")
		}
		tCancel()
		_ = cc.Close()
		bidiConn.CloseStream()
		return nil
	}
	defer func() {
		tm.unregisterActiveTunnel(mapKey, at)
		_ = cc.Close()
		bidiConn.CloseStream()
		bidiConn.WaitWriteDone(2 * time.Second)
	}()

	maintDone := tm.startTunnelLifetimeAndMaintenance(tCtx, tCancel, at, user, func() error {
		if ccErr := cc.Err(); ccErr != nil {
			at.sendCloseFn(pb.CloseCode_CLOSE_CODE_HEARTBEAT_TIMEOUT, "tunnel HTTP/2 connection unhealthy")
			return ccErr
		}
		return nil
	})

	waitMaint := func() {
		select {
		case <-maintDone:
		case <-time.After(500 * time.Millisecond):
		}
	}

	select {
	case <-tCtx.Done():
		if errors.Is(tCtx.Err(), context.DeadlineExceeded) && at.sendCloseFn != nil {
			at.sendCloseFn(pb.CloseCode_CLOSE_CODE_AUTH_EXPIRED, "tunnel session lifetime or token expired")
		}
		waitMaint()
		bidiConn.CloseStream()
		_ = cc.Close()
		bidiConn.WaitDone(150 * time.Millisecond)
		bidiConn.WaitWriteDone(2 * time.Second)
		return nil
	case <-bidiConn.Done():
		tCancel()
		if evt, ok := bidiConn.RemoteClose(); ok {
			tm.logger.Info("tunnel closed by client", "code", evt.Code.String(), "reason", evt.Reason, "preview-id", at.previewID)
		}
		waitMaint()
		bidiConn.CloseStream()
		_ = cc.Close()
		bidiConn.WaitWriteDone(2 * time.Second)
		return nil
	}
}

func (tm *TunnelManager) runV2Tunnel(
	tCtx context.Context,
	tCancel context.CancelFunc,
	stream *connect.BidiStream[pb.TunnelServiceTunnelRequest, pb.TunnelServiceTunnelResponse],
	at *activeTunnel,
	ready *pb.TunnelReady,
	user *auth.UserInfo,
) error {
	at.reqCh = make(chan *pb.TunnelHTTPRequest, tunnelReqChBuffer)
	at.respWg = make(map[string]chan *pb.TunnelHTTPResponse)
	at.chunkBuffer = make(map[string]*chunkAssembly)

	var sendMu sync.Mutex
	var v2Closed bool
	var closeSent bool
	at.sendCloseFn = func(code pb.CloseCode, reason string) {
		sendMu.Lock()
		defer sendMu.Unlock()
		if v2Closed || closeSent {
			return
		}
		closeSent = true
		_ = stream.Send(&pb.TunnelServiceTunnelResponse{
			Payload: &pb.TunnelServiceTunnelResponse_Close{
				Close: &pb.TunnelClose{
					Code:   code,
					Reason: reason,
				},
			},
		})
	}

	mapKey, ok := tm.registerActiveTunnel(at)
	if !ok {
		if tm.shuttingDown.Load() {
			at.sendCloseFn(pb.CloseCode_CLOSE_CODE_SERVER_SHUTDOWN, "server shutting down")
		} else {
			at.sendCloseFn(pb.CloseCode_CLOSE_CODE_EVICTED, "evicted by replacement tunnel")
		}
		tCancel()
		return nil
	}
	defer func() {
		tm.unregisterActiveTunnel(mapKey, at)
		sendMu.Lock()
		v2Closed = true
		sendMu.Unlock()

		at.respMu.Lock()
		at.respWg = make(map[string]chan *pb.TunnelHTTPResponse)
		at.respMu.Unlock()
	}()

	sendMu.Lock()
	err := stream.Send(&pb.TunnelServiceTunnelResponse{
		Payload: &pb.TunnelServiceTunnelResponse_Ready{Ready: ready},
	})
	sendMu.Unlock()
	if err != nil {
		return err
	}

	maintDone := tm.startTunnelLifetimeAndMaintenance(tCtx, tCancel, at, user, func() error {
		at.lastPongMu.RLock()
		stale := time.Since(at.lastPong) > tunnelHeartbeatTimeout
		at.lastPongMu.RUnlock()
		if stale {
			tm.logger.Warn("tunnel heartbeat timeout, closing", "preview-id", at.previewID)
			at.sendCloseFn(pb.CloseCode_CLOSE_CODE_HEARTBEAT_TIMEOUT, "heartbeat timeout")
			return errors.New("heartbeat timeout")
		}
		sendMu.Lock()
		if !v2Closed {
			_ = stream.Send(&pb.TunnelServiceTunnelResponse{
				Payload: &pb.TunnelServiceTunnelResponse_Ping{
					Ping: &pb.TunnelPing{Timestamp: timestamppb.Now()},
				},
			})
		}
		sendMu.Unlock()
		return nil
	})

	errCh := make(chan error, 1)
	recvDone := make(chan struct{})
	go func() {
		defer close(recvDone)
		defer func() {
			if r := recover(); r != nil {
				tm.logger.Error("panic in receive goroutine", "err", r)
				errCh <- fmt.Errorf("panic: %v", r)
			}
		}()

		for {
			msg, recvErr := stream.Receive()
			if recvErr != nil {
				errCh <- recvErr
				return
			}
			switch p := msg.Payload.(type) {
			case *pb.TunnelServiceTunnelRequest_HttpResponse:
				if p.HttpResponse.HasMoreChunks {
					at.respMu.RLock()
					_, ok := at.respWg[p.HttpResponse.RequestId]
					at.respMu.RUnlock()
					if ok {
						at.chunkMu.Lock()
						at.chunkBuffer[p.HttpResponse.RequestId] = &chunkAssembly{
							statusCode: p.HttpResponse.StatusCode,
							headers:    p.HttpResponse.Headers,
							body:       p.HttpResponse.Body,
						}
						at.chunkMu.Unlock()
					}
				} else {
					at.respMu.RLock()
					ch, ok := at.respWg[p.HttpResponse.RequestId]
					at.respMu.RUnlock()
					if ok {
						select {
						case ch <- p.HttpResponse:
						default:
						}
					}
				}
			case *pb.TunnelServiceTunnelRequest_ResponseChunk:
				at.chunkMu.Lock()
				assembly := at.chunkBuffer[p.ResponseChunk.RequestId]
				if assembly == nil {
					at.chunkMu.Unlock()
					continue
				}
				assembly.body = append(assembly.body, p.ResponseChunk.Data...)
				if len(assembly.body) > maxChunkBufferSize {
					delete(at.chunkBuffer, p.ResponseChunk.RequestId)
					at.chunkMu.Unlock()
					tm.logger.Warn("chunk buffer exceeded max size", "request_id", p.ResponseChunk.RequestId)
					continue
				}
				if p.ResponseChunk.IsLast {
					delete(at.chunkBuffer, p.ResponseChunk.RequestId)
					at.chunkMu.Unlock()

					at.respMu.RLock()
					ch, ok := at.respWg[p.ResponseChunk.RequestId]
					at.respMu.RUnlock()
					if ok {
						select {
						case ch <- &pb.TunnelHTTPResponse{
							RequestId:  p.ResponseChunk.RequestId,
							StatusCode: assembly.statusCode,
							Headers:    assembly.headers,
							Body:       assembly.body,
						}:
						default:
						}
					}
				} else {
					at.chunkMu.Unlock()
				}
			case *pb.TunnelServiceTunnelRequest_Pong:
				at.lastPongMu.Lock()
				at.lastPong = time.Now()
				at.lastPongMu.Unlock()
			case *pb.TunnelServiceTunnelRequest_Close:
				tm.logger.Info("tunnel closed by client", "reason", p.Close.Reason, "preview-id", at.previewID)
				errCh <- nil
				return
			}
		}
	}()

	waitMaint := func() {
		select {
		case <-maintDone:
		case <-time.After(500 * time.Millisecond):
		}
	}

	for {
		select {
		case <-tCtx.Done():
			if errors.Is(tCtx.Err(), context.DeadlineExceeded) && at.sendCloseFn != nil {
				at.sendCloseFn(pb.CloseCode_CLOSE_CODE_AUTH_EXPIRED, "tunnel session lifetime or token expired")
			}
			tCancel()
			select {
			case <-recvDone:
			case <-time.After(150 * time.Millisecond):
			}
			waitMaint()
			return nil
		case recvErr := <-errCh:
			tCancel()
			waitMaint()
			return recvErr
		case req := <-at.reqCh:
			sendMu.Lock()
			if v2Closed {
				sendMu.Unlock()
				return nil
			}
			sendErr := stream.Send(&pb.TunnelServiceTunnelResponse{
				Payload: &pb.TunnelServiceTunnelResponse_HttpRequest{
					HttpRequest: req,
				},
			})
			sendMu.Unlock()
			if sendErr != nil {
				return sendErr
			}
		}
	}
}

func (tm *TunnelManager) canShareForeignServices(owner string, declaredServices []string, targetService string, protoVer int32) bool {
	if protoVer < 3 {
		return false
	}
	if owner == "" || !tm.sharedPreviewOwners[owner] {
		return false
	}
	if targetService == "" || len(declaredServices) == 0 {
		return false
	}
	return !slices.Contains(declaredServices, targetService)
}

func (tm *TunnelManager) authorizePreviewGroupRBAC(ctx context.Context, verb, fallbackNS string) error {
	if err := AuthorizeAction(ctx, tm.k8sClient, tm.auditLogger, verb, "", "previewgroups"); err == nil {
		return nil
	}
	return AuthorizeAction(ctx, tm.k8sClient, tm.auditLogger, verb, fallbackNS, "previewgroups")
}

func (tm *TunnelManager) authorizeCrossUserPreviewGroupForce(ctx context.Context, namespace string, pg *divergeiov1alpha1.PreviewGroup) error {
	for _, verb := range []string{"update", "delete"} {
		if err := AuthorizeAction(ctx, tm.k8sClient, tm.auditLogger, verb, namespace, "environments"); err != nil {
			return err
		}
		if err := tm.authorizePreviewGroupRBAC(ctx, verb, namespace); err != nil {
			return err
		}
	}
	seenNS := make(map[string]struct{})
	for _, svc := range pg.Spec.Services {
		svcNS := strings.TrimSpace(svc.Namespace)
		if svcNS == "" {
			svcNS = "default"
		}
		if _, done := seenNS[svcNS]; done {
			continue
		}
		seenNS[svcNS] = struct{}{}
		for _, verb := range []string{"update", "delete"} {
			if err := AuthorizePreviewGroupServiceNamespace(ctx, tm.k8sClient, tm.auditLogger, verb, svcNS, namespace); err != nil {
				return err
			}
		}
	}
	return nil
}

func declaredEnvironmentServices(env *divergeiov1alpha1.Environment) []string {
	var svcs []string
	if env.Spec.ServiceConfig != nil && env.Spec.ServiceConfig.ServiceName != "" {
		svcs = append(svcs, env.Spec.ServiceConfig.ServiceName)
	}
	for _, s := range env.Spec.Deploy.ChangedServices {
		if s != "" && !slices.Contains(svcs, s) {
			svcs = append(svcs, s)
		}
	}
	return svcs
}

func declaredPreviewGroupServices(pg *divergeiov1alpha1.PreviewGroup) []string {
	var svcs []string
	for _, s := range pg.Spec.Services {
		if s.Name != "" && !slices.Contains(svcs, s.Name) {
			svcs = append(svcs, s.Name)
		}
	}
	return svcs
}

// validatePreviewOwnership verifies that the authenticated user owns any existing
// cluster-scoped PreviewGroup or namespaced Environment matching preview_id,
// allowing unforced cross-service attachment only onto allowlisted CI shared previews.
func (tm *TunnelManager) validatePreviewOwnership(ctx context.Context, reg *pb.TunnelRegister) error {
	user, ok := auth.UserInfoFromContext(ctx)
	if !ok {
		return fmt.Errorf("no authenticated user in context")
	}
	if tm.crdClient == nil {
		return nil
	}

	namespace := reg.Namespace
	if namespace == "" {
		namespace = tm.effectiveDefaultNamespace()
	}

	var matchedGroups []divergeiov1alpha1.PreviewGroup
	var directPG divergeiov1alpha1.PreviewGroup
	if err := tm.crdClient.Get(ctx, client.ObjectKey{Namespace: "", Name: reg.PreviewId}, &directPG); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to look up preview group: %w", err)
		}
	} else {
		matchedGroups = append(matchedGroups, directPG)
	}

	var pgList divergeiov1alpha1.PreviewGroupList
	if err := tm.crdClient.List(ctx, &pgList); err != nil {
		return fmt.Errorf("failed to list preview groups: %w", err)
	}
	for _, item := range pgList.Items {
		if item.Name != reg.PreviewId && item.Spec.Routing.HeaderValue == reg.PreviewId {
			matchedGroups = append(matchedGroups, item)
		}
	}

	for i := range matchedGroups {
		pg := &matchedGroups[i]
		owner := pg.Spec.Owner
		if owner == "" || owner == user.Username {
			continue
		}
		if tm.canShareForeignServices(owner, declaredPreviewGroupServices(pg), reg.Service, reg.ProtocolVersion) {
			continue
		}
		if reg.Force {
			if err := tm.authorizeCrossUserPreviewGroupForce(ctx, namespace, pg); err != nil {
				return err
			}
			continue
		}
		return fmt.Errorf("preview-id %q is owned by %q", reg.PreviewId, owner)
	}

	var matchedEnvs []divergeiov1alpha1.Environment
	var directEnv divergeiov1alpha1.Environment
	if err := tm.crdClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: reg.PreviewId}, &directEnv); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to look up environment: %w", err)
		}
	} else {
		matchedEnvs = append(matchedEnvs, directEnv)
	}

	var envList divergeiov1alpha1.EnvironmentList
	if err := tm.crdClient.List(ctx, &envList, client.InNamespace(namespace)); err != nil {
		return fmt.Errorf("failed to list environments: %w", err)
	}
	for _, item := range envList.Items {
		if item.Name != reg.PreviewId && item.Spec.Routing.HeaderValue == reg.PreviewId {
			matchedEnvs = append(matchedEnvs, item)
		}
	}

	for i := range matchedEnvs {
		env := &matchedEnvs[i]
		owner := env.Annotations["divergedev.com/created-by"]
		if owner == "" || owner == user.Username {
			continue
		}
		if tm.canShareForeignServices(owner, declaredEnvironmentServices(env), reg.Service, reg.ProtocolVersion) {
			continue
		}
		if reg.Force {
			if err := AuthorizeAction(ctx, tm.k8sClient, tm.auditLogger, "update", namespace, "environments"); err != nil {
				return err
			}
			if err := AuthorizeAction(ctx, tm.k8sClient, tm.auditLogger, "delete", namespace, "environments"); err != nil {
				return err
			}
			continue
		}
		return fmt.Errorf("preview-id %q is owned by %q", reg.PreviewId, owner)
	}

	return nil
}

// evictLocalTunnel cancels matching tunnels on this pod (targeted by previousHolder when available)
// and asynchronously delivers CLOSE_CODE_EVICTED without blocking replacement setup.
func (tm *TunnelManager) evictLocalTunnel(namespace, leaseName, previousHolder, reason string) {
	type victimEntry struct {
		key    string
		tunnel *activeTunnel
	}
	var victims []victimEntry
	tm.mu.Lock()
	for key, at := range tm.tunnels {
		if at == nil {
			continue
		}
		matched := false
		if previousHolder != "" {
			matched = tm.holderIdentity(at.tunnelID) == previousHolder
		} else {
			matched = at.namespace == namespace && at.leaseName == leaseName
		}
		if matched {
			victims = append(victims, victimEntry{key: key, tunnel: at})
			delete(tm.tunnels, key)
			decActiveTunnels(at.namespace)
		}
	}
	if len(victims) == 0 && previousHolder != "" && strings.HasPrefix(previousHolder, tm.podName+":") {
		if tm.evictedHolders == nil {
			tm.evictedHolders = make(map[string]struct{})
		}
		tm.evictedHolders[previousHolder] = struct{}{}
	}
	tm.mu.Unlock()

	for _, v := range victims {
		existing := v.tunnel
		tm.logger.Info("evicting local tunnel", "namespace", namespace, "preview-id", existing.previewID, "service", existing.service, "tunnel-id", existing.tunnelID)
		go func(t *activeTunnel) {
			if t.sendCloseFn != nil {
				sent := make(chan struct{})
				go func() {
					defer close(sent)
					t.sendCloseFn(pb.CloseCode_CLOSE_CODE_EVICTED, reason)
				}()
				timer := time.NewTimer(250 * time.Millisecond)
				select {
				case <-sent:
					timer.Stop()
				case <-timer.C:
				}
			}
			t.cancel()
		}(existing)
	}
}

func (tm *TunnelManager) ensureLeaseHolder(ctx context.Context, namespace, leaseName, tunnelID string) error {
	if tm.lease == nil || leaseName == "" {
		return nil
	}
	held, err := tm.lease.IsCurrentHolder(ctx, namespace, leaseName, tunnelID)
	if err != nil {
		return fmt.Errorf("failed to verify tunnel lease ownership: %w", err)
	}
	if !held {
		return fmt.Errorf("tunnel lease %q ownership lost to another tunnel", leaseName)
	}
	return nil
}

func (tm *TunnelManager) createTunnelResources(
	ctx context.Context,
	reg *pb.TunnelRegister,
	svcName, podIP, tunnelID string,
	leaseRes *LeaseAcquireResult,
) error {
	if tm.k8sClient == nil {
		return nil
	}

	leaseSvc := reg.Service
	if reg.ProtocolVersion < 3 {
		leaseSvc = ""
	}
	leaseName := tunnelLeaseName(reg.PreviewId, leaseSvc)
	if leaseRes != nil && leaseRes.LeaseName != "" {
		leaseName = leaseRes.LeaseName
	}
	if err := tm.ensureLeaseHolder(ctx, reg.Namespace, leaseName, tunnelID); err != nil {
		return err
	}

	var gen int32
	var ownerRefs []metav1.OwnerReference
	if leaseRes != nil {
		gen = leaseRes.LeaseTransitions
		if leaseRes.LeaseUID != "" {
			ownerRefs = []metav1.OwnerReference{{
				APIVersion: "coordination.k8s.io/v1",
				Kind:       "Lease",
				Name:       leaseRes.LeaseName,
				UID:        leaseRes.LeaseUID,
			}}
		}
	}

	labels := map[string]string{
		"divergedev.com/tunnel":     "true",
		"divergedev.com/preview-id": reg.PreviewId,
	}
	if reg.Service != "" {
		labels["divergedev.com/service"] = reg.Service
	}
	genStr := strconv.FormatInt(int64(gen), 10)
	annotations := map[string]string{
		"divergedev.com/tunnel-id":         tunnelID,
		"divergedev.com/tunnel-holder":     tm.podName,
		"divergedev.com/tunnel-generation": genStr,
	}
	epAnnotations := map[string]string{
		"divergedev.com/tunnel-id":         tunnelID,
		"divergedev.com/tunnel-generation": genStr,
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            svcName,
			Namespace:       reg.Namespace,
			Labels:          labels,
			Annotations:     annotations,
			OwnerReferences: ownerRefs,
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{{
				Port:       reg.Port,
				TargetPort: intstr.FromInt32(TunnelProxyPort),
				Protocol:   corev1.ProtocolTCP,
			}},
		},
	}

	proxyPort := int32(TunnelProxyPort)
	epLabels := make(map[string]string, len(labels)+1)
	for k, v := range labels {
		epLabels[k] = v
	}
	epLabels["kubernetes.io/service-name"] = svcName

	ep := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:            svcName,
			Namespace:       reg.Namespace,
			Labels:          epLabels,
			Annotations:     epAnnotations,
			OwnerReferences: ownerRefs,
		},
		AddressType: discoveryv1.AddressTypeIPv4,
		Endpoints: []discoveryv1.Endpoint{
			{
				Addresses: []string{podIP},
				Conditions: discoveryv1.EndpointConditions{
					Ready: func() *bool { b := true; return &b }(),
				},
			},
		},
		Ports: []discoveryv1.EndpointPort{
			{
				Port:     &proxyPort,
				Protocol: func() *corev1.Protocol { p := corev1.ProtocolTCP; return &p }(),
			},
		},
	}

	if _, err := tm.k8sClient.CoreV1().Services(reg.Namespace).Create(ctx, svc, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create service: %w", err)
		}
		existing, getErr := tm.k8sClient.CoreV1().Services(reg.Namespace).Get(ctx, svc.Name, metav1.GetOptions{})
		if getErr != nil {
			return fmt.Errorf("failed to get existing service: %w", getErr)
		}
		if existing.Labels["divergedev.com/tunnel"] != "true" {
			return fmt.Errorf("refusing to overwrite non-tunnel service %q", svc.Name)
		}
		if err := tm.ensureLeaseHolder(ctx, reg.Namespace, leaseName, tunnelID); err != nil {
			return err
		}
		if existing.Spec.ClusterIP == corev1.ClusterIPNone {
			if delErr := tm.k8sClient.CoreV1().Services(reg.Namespace).Delete(ctx, svc.Name, deletePreconditions(existing.UID, existing.ResourceVersion)); delErr != nil && !apierrors.IsNotFound(delErr) {
				return fmt.Errorf("failed to delete legacy headless service: %w", delErr)
			}
			if _, createErr := tm.k8sClient.CoreV1().Services(reg.Namespace).Create(ctx, svc, metav1.CreateOptions{}); createErr != nil && !apierrors.IsAlreadyExists(createErr) {
				return fmt.Errorf("failed to recreate service as ClusterIP: %w", createErr)
			}
		} else {
			svc.ResourceVersion = existing.ResourceVersion
			svc.Spec.ClusterIP = existing.Spec.ClusterIP
			if _, updateErr := tm.k8sClient.CoreV1().Services(reg.Namespace).Update(ctx, svc, metav1.UpdateOptions{}); updateErr != nil {
				return fmt.Errorf("failed to update service: %w", updateErr)
			}
		}
	}

	if _, err := tm.k8sClient.DiscoveryV1().EndpointSlices(reg.Namespace).Create(ctx, ep, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create endpointslice: %w", err)
		}
		existing, getErr := tm.k8sClient.DiscoveryV1().EndpointSlices(reg.Namespace).Get(ctx, ep.Name, metav1.GetOptions{})
		if getErr != nil {
			return fmt.Errorf("failed to get existing endpointslice: %w", getErr)
		}
		if existing.Labels["divergedev.com/tunnel"] != "true" {
			return fmt.Errorf("refusing to overwrite non-tunnel endpointslice %q", ep.Name)
		}
		if err := tm.ensureLeaseHolder(ctx, reg.Namespace, leaseName, tunnelID); err != nil {
			return err
		}
		ep.ResourceVersion = existing.ResourceVersion
		if _, updateErr := tm.k8sClient.DiscoveryV1().EndpointSlices(reg.Namespace).Update(ctx, ep, metav1.UpdateOptions{}); updateErr != nil {
			return fmt.Errorf("failed to update endpointslice: %w", updateErr)
		}
	}

	endpoints := &corev1.Endpoints{ //nolint:staticcheck // kube-dns resolves Services only from core/v1 Endpoints
		ObjectMeta: metav1.ObjectMeta{
			Name:            svcName,
			Namespace:       reg.Namespace,
			Labels:          labels,
			Annotations:     epAnnotations,
			OwnerReferences: ownerRefs,
		},
		Subsets: []corev1.EndpointSubset{{ //nolint:staticcheck // see above
			Addresses: []corev1.EndpointAddress{{IP: podIP}},
			Ports:     []corev1.EndpointPort{{Port: proxyPort, Protocol: corev1.ProtocolTCP}},
		}},
	}
	if _, err := tm.k8sClient.CoreV1().Endpoints(reg.Namespace).Create(ctx, endpoints, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create endpoints: %w", err)
		}
		existing, getErr := tm.k8sClient.CoreV1().Endpoints(reg.Namespace).Get(ctx, endpoints.Name, metav1.GetOptions{})
		if getErr != nil {
			return fmt.Errorf("failed to get existing endpoints: %w", getErr)
		}
		if existing.Labels["divergedev.com/tunnel"] != "true" {
			return fmt.Errorf("refusing to overwrite non-tunnel endpoints %q", endpoints.Name)
		}
		if err := tm.ensureLeaseHolder(ctx, reg.Namespace, leaseName, tunnelID); err != nil {
			return err
		}
		endpoints.ResourceVersion = existing.ResourceVersion
		if _, updateErr := tm.k8sClient.CoreV1().Endpoints(reg.Namespace).Update(ctx, endpoints, metav1.UpdateOptions{}); updateErr != nil {
			return fmt.Errorf("failed to update endpoints: %w", updateErr)
		}
	}

	return nil
}

func (tm *TunnelManager) deleteTunnelResources(ctx context.Context, namespace, svcName, tunnelID string) {
	if tm.k8sClient == nil || svcName == "" {
		return
	}
	if svc, err := tm.k8sClient.CoreV1().Services(namespace).Get(ctx, svcName, metav1.GetOptions{}); err == nil {
		if svc.Labels["divergedev.com/tunnel"] == "true" && svc.Annotations["divergedev.com/tunnel-id"] == tunnelID {
			_ = tm.k8sClient.CoreV1().Services(namespace).Delete(ctx, svcName, deletePreconditions(svc.UID, svc.ResourceVersion))
		}
	}
	if ep, err := tm.k8sClient.DiscoveryV1().EndpointSlices(namespace).Get(ctx, svcName, metav1.GetOptions{}); err == nil {
		if ep.Labels["divergedev.com/tunnel"] == "true" && ep.Annotations["divergedev.com/tunnel-id"] == tunnelID {
			_ = tm.k8sClient.DiscoveryV1().EndpointSlices(namespace).Delete(ctx, svcName, deletePreconditions(ep.UID, ep.ResourceVersion))
		}
	}
	if eps, err := tm.k8sClient.CoreV1().Endpoints(namespace).Get(ctx, svcName, metav1.GetOptions{}); err == nil { //nolint:staticcheck // cleaning up legacy Endpoints
		if eps.Labels["divergedev.com/tunnel"] == "true" && eps.Annotations["divergedev.com/tunnel-id"] == tunnelID {
			_ = tm.k8sClient.CoreV1().Endpoints(namespace).Delete(ctx, svcName, deletePreconditions(eps.UID, eps.ResourceVersion))
		}
	}
}

func (tm *TunnelManager) findActiveTunnel(namespace, previewID, service, hostSvcName string) (at *activeTunnel, matchCount int, ok bool) {
	if previewID == "" && hostSvcName == "" {
		return nil, 0, false
	}

	tm.mu.RLock()
	defer tm.mu.RUnlock()

	// Match per-tunnel K8s Service Host header first (covers hashed multi-service
	// diverge-tsvc-* and unnamed diverge-tunnel-* hosts even when X-Diverge-Env is set).
	if hostSvcName != "" {
		for _, cand := range tm.tunnels {
			if cand == nil || cand.k8sSvcName != hostSvcName {
				continue
			}
			if namespace != "" && cand.namespace != namespace {
				continue
			}
			if previewID != "" && cand.previewID != previewID {
				continue
			}
			if service != "" && cand.service != service {
				continue
			}
			if at != nil {
				matchCount++
			} else {
				at = cand
				matchCount = 1
			}
		}
		if matchCount == 1 {
			return at, 1, true
		}
		if matchCount > 1 {
			return nil, matchCount, false
		}
	}

	// Exact match when namespace and service are explicitly specified.
	if namespace != "" && service != "" {
		if exact, found := tm.tunnels[tunnelMapKey(namespace, previewID, service)]; found && exact != nil {
			return exact, 1, true
		}
		if v2Exact, found := tm.tunnels[tunnelMapKey(namespace, previewID, "")]; found && v2Exact != nil && v2Exact.protocolVersion < 3 && v2Exact.service == service {
			return v2Exact, 1, true
		}
		return nil, 0, false
	}

	if namespace != "" && service == "" {
		if exact, found := tm.tunnels[tunnelMapKey(namespace, previewID, "")]; found && exact != nil {
			return exact, 1, true
		}
	}

	for _, cand := range tm.tunnels {
		if cand == nil || cand.previewID != previewID {
			continue
		}
		if namespace != "" && cand.namespace != namespace {
			continue
		}
		if service != "" && cand.service != service {
			continue
		}
		matchCount++
		at = cand
	}
	if matchCount == 1 {
		return at, 1, true
	}
	return nil, matchCount, false
}

func (tm *TunnelManager) getActiveTunnelInNamespace(namespace, previewID string) (*activeTunnel, bool) {
	if namespace == "" {
		return tm.getActiveTunnel(previewID)
	}
	at, _, ok := tm.findActiveTunnel(namespace, previewID, "", "")
	return at, ok
}

func (tm *TunnelManager) getActiveTunnel(previewID string) (*activeTunnel, bool) {
	if previewID == "" {
		return nil, false
	}
	at, _, ok := tm.findActiveTunnel("", previewID, "", "")
	return at, ok
}

// ForwardRequest sends an HTTP request through the tunnel to the CLI (legacy v2).
func (tm *TunnelManager) ForwardRequest(ctx context.Context, previewID string, req *pb.TunnelHTTPRequest) (*pb.TunnelHTTPResponse, error) {
	at, ok := tm.getActiveTunnel(previewID)
	if !ok {
		return nil, fmt.Errorf("tunnel not found for preview-id: %s", previewID)
	}
	return tm.forwardToActiveTunnel(ctx, at, req)
}

// ForwardRequestInNamespace sends an HTTP request through the tunnel in a specific namespace (legacy v2).
func (tm *TunnelManager) ForwardRequestInNamespace(ctx context.Context, namespace, previewID string, req *pb.TunnelHTTPRequest) (*pb.TunnelHTTPResponse, error) {
	at, ok := tm.getActiveTunnelInNamespace(namespace, previewID)
	if !ok {
		return nil, fmt.Errorf("tunnel not found for namespace %s preview-id: %s", namespace, previewID)
	}
	return tm.forwardToActiveTunnel(ctx, at, req)
}

func (tm *TunnelManager) forwardToActiveTunnel(ctx context.Context, at *activeTunnel, req *pb.TunnelHTTPRequest) (*pb.TunnelHTTPResponse, error) {
	if at.reqCh == nil {
		return nil, fmt.Errorf("tunnel requires Protocol v3 HTTP/2 transport")
	}
	respCh := make(chan *pb.TunnelHTTPResponse, 1)
	at.respMu.Lock()
	at.respWg[req.RequestId] = respCh
	at.respMu.Unlock()

	defer func() {
		at.respMu.Lock()
		delete(at.respWg, req.RequestId)
		at.respMu.Unlock()
		at.chunkMu.Lock()
		delete(at.chunkBuffer, req.RequestId)
		at.chunkMu.Unlock()
	}()

	select {
	case at.reqCh <- req:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-at.ctx.Done():
		return nil, fmt.Errorf("tunnel disconnected")
	case <-time.After(5 * time.Second):
		return nil, fmt.Errorf("tunnel queue full, request dropped")
	}

	timeout := time.NewTimer(30 * time.Second)
	defer timeout.Stop()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-at.ctx.Done():
		return nil, fmt.Errorf("tunnel disconnected while waiting for response")
	case <-timeout.C:
		return nil, fmt.Errorf("timeout waiting for tunnel response")
	case resp, ok := <-respCh:
		if !ok {
			return nil, fmt.Errorf("tunnel disconnected")
		}
		return resp, nil
	}
}

func (tm *TunnelManager) HasTunnel(previewID string) bool {
	_, ok := tm.getActiveTunnel(previewID)
	return ok
}

func (tm *TunnelManager) HasTunnelInNamespace(namespace, previewID string) bool {
	_, ok := tm.getActiveTunnelInNamespace(namespace, previewID)
	return ok
}

type trailerStrippingBody struct {
	io.ReadCloser
	trailer http.Header
}

func (b *trailerStrippingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		stripDivergeHeadersAndTrailers(b.trailer)
	}
	return n, err
}

func stripDivergeHeadersAndTrailers(h http.Header) {
	if h == nil {
		return
	}
	for k := range h {
		trimmed := strings.TrimPrefix(k, http.TrailerPrefix)
		if strings.HasPrefix(strings.ToLower(trimmed), "x-diverge-") {
			delete(h, k)
		}
	}
	if vals := h.Values("Trailer"); len(vals) > 0 {
		var kept []string
		for _, raw := range vals {
			for _, tok := range strings.Split(raw, ",") {
				name := strings.TrimSpace(tok)
				if name == "" || strings.HasPrefix(strings.ToLower(name), "x-diverge-") {
					continue
				}
				kept = append(kept, name)
			}
		}
		if len(kept) == 0 {
			h.Del("Trailer")
		} else {
			h["Trailer"] = kept
		}
	}
}

func connectionHeaderTokens(values []string) map[string]bool {
	var tokens map[string]bool
	for _, raw := range values {
		for _, part := range strings.Split(raw, ",") {
			tok := strings.ToLower(strings.TrimSpace(part))
			if tok == "" {
				continue
			}
			if tokens == nil {
				tokens = make(map[string]bool)
			}
			tokens[tok] = true
		}
	}
	return tokens
}

func isHopByHopHeader(key string, connTokens map[string]bool) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	switch lower {
	case "connection", "proxy-connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	}
	return connTokens[lower]
}

type tunnelStatusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *tunnelStatusRecorder) WriteHeader(code int) {
	if !r.wroteHeader && code >= 200 {
		r.status = code
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *tunnelStatusRecorder) Flush() {
	if fl, ok := r.ResponseWriter.(http.Flusher); ok {
		fl.Flush()
	}
}

func (r *tunnelStatusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// NewTunnelProxyHandler creates the HTTP handler for the cluster-internal tunnel proxy (:8081).
func (tm *TunnelManager) NewTunnelProxyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &tunnelStatusRecorder{ResponseWriter: w, status: http.StatusOK}
		metricNS := "unknown"
		defer func() {
			recordTunnelRequest(metricNS, rec.status, time.Since(start))
		}()
		w = rec

		parsedHostSvc, parsedHostPreview, parsedHostNS := extractServicePreviewAndNamespaceFromHost(r.Host)
		headerEnv := strings.TrimSpace(r.Header.Get("X-Diverge-Env"))
		previewID := headerEnv
		if previewID == "" {
			previewID = parsedHostPreview
		}
		namespace := strings.TrimSpace(r.Header.Get("X-Diverge-Namespace"))
		if namespace == "" {
			if headerEnv == "" || strings.HasPrefix(parsedHostSvc, "diverge-tsvc-") || (parsedHostPreview != "" && parsedHostPreview == headerEnv) {
				namespace = parsedHostNS
			}
		}
		if namespace == "" && tm.defaultNamespace != "" && headerEnv != "" {
			namespace = tm.defaultNamespace
		}
		service := strings.TrimSpace(r.Header.Get("X-Diverge-Service"))

		if previewID == "" && parsedHostSvc == "" {
			http.Error(w, "cannot determine tunnel target", http.StatusBadRequest)
			return
		}
		if previewID != "" {
			if err := ValidateDNS1123Label(previewID, "preview_id"); err != nil {
				http.Error(w, "invalid preview_id", http.StatusBadRequest)
				return
			}
		}
		if namespace != "" {
			if err := ValidateDNS1123Label(namespace, "namespace"); err != nil {
				http.Error(w, "invalid namespace", http.StatusBadRequest)
				return
			}
			if !tm.isNamespaceAllowed(namespace) {
				http.Error(w, "namespace not allowed", http.StatusForbidden)
				return
			}
			if len(tm.allowedNamespaces) > 0 {
				metricNS = namespace
			}
		}
		if service != "" {
			if err := ValidateDNS1123Label(service, "service"); err != nil {
				http.Error(w, "invalid service", http.StatusBadRequest)
				return
			}
		}

		at, matchCount, ok := tm.findActiveTunnel(namespace, previewID, service, parsedHostSvc)
		if !ok {
			if matchCount > 1 && namespace != "" && service == "" {
				http.Error(w, "ambiguous tunnel target; X-Diverge-Service header required", http.StatusBadRequest)
				return
			}
			http.Error(w, "tunnel not found", http.StatusBadGateway)
			return
		}
		if !tm.isNamespaceAllowed(at.namespace) {
			http.Error(w, "namespace not allowed", http.StatusForbidden)
			return
		}
		metricNS = at.namespace

		if at.protocolVersion >= 3 && at.proxy != nil {
			if r.ContentLength > maxProxyRequestBodySize {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			select {
			case at.sem <- struct{}{}:
				defer func() { <-at.sem }()
			default:
				http.Error(w, "tunnel concurrency limit reached", http.StatusServiceUnavailable)
				return
			}
			at.reqMu.RLock()
			if at.reqDraining {
				at.reqMu.RUnlock()
				http.Error(w, "tunnel shutting down", http.StatusServiceUnavailable)
				return
			}
			at.reqWg.Add(1)
			at.reqMu.RUnlock()
			defer at.reqWg.Done()

			r.Body = http.MaxBytesReader(w, r.Body, maxProxyRequestBodySize)
			at.proxy.ServeHTTP(w, r)
			stripDivergeHeadersAndTrailers(w.Header())
			return
		}

		// Legacy Protocol v2 buffered forwarding (1 MiB body limit, bounded read deadline)
		const maxV2ProxyBodySize = 1 << 20
		rc := http.NewResponseController(w)
		_ = rc.SetReadDeadline(time.Now().Add(30 * time.Second))
		body, err := io.ReadAll(io.LimitReader(r.Body, maxV2ProxyBodySize+1))
		_ = rc.SetReadDeadline(time.Time{})
		if err != nil {
			http.Error(w, "failed to read request body", http.StatusBadRequest)
			return
		}
		if len(body) > maxV2ProxyBodySize {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}

		reqID := uuid.New().String()
		tunnelReq := &pb.TunnelHTTPRequest{
			RequestId: reqID,
			Method:    r.Method,
			Path:      r.URL.RequestURI(),
			Headers:   headersToProto(r.Header),
			Body:      body,
		}

		resp, fwdErr := tm.forwardToActiveTunnel(r.Context(), at, tunnelReq)
		if fwdErr != nil {
			tm.logger.Error("tunnel forward error", "err", fwdErr, "preview_id", previewID, "namespace", namespace)
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}

		if resp.StatusCode < 200 || resp.StatusCode > 599 {
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}
		copyFilteredV2ResponseHeaders(w.Header(), resp.Headers)
		w.WriteHeader(int(resp.StatusCode))
		_, _ = w.Write(resp.Body)
	})
}

func extractServicePreviewAndNamespaceFromHost(host string) (svcName, previewID, namespace string) {
	if idx := strings.IndexByte(host, ':'); idx != -1 {
		host = host[:idx]
	}

	firstLabel := host
	var remainder string
	if dotIdx := strings.IndexByte(host, '.'); dotIdx != -1 {
		firstLabel = host[:dotIdx]
		remainder = host[dotIdx+1:]
	}
	if remainder != "" {
		if nextDot := strings.IndexByte(remainder, '.'); nextDot != -1 {
			namespace = remainder[:nextDot]
		} else {
			namespace = remainder
		}
	}

	const hashedSvcPrefix = "diverge-tsvc-"
	if strings.HasPrefix(firstLabel, hashedSvcPrefix) {
		if len(firstLabel) <= len(hashedSvcPrefix) {
			return "", "", ""
		}
		return firstLabel, "", namespace
	}

	const legacyPrefix = "diverge-tunnel-"
	if !strings.HasPrefix(firstLabel, legacyPrefix) {
		return "", "", ""
	}
	previewID = firstLabel[len(legacyPrefix):]
	if previewID == "" {
		return "", "", ""
	}
	return firstLabel, previewID, namespace
}

// extractPreviewAndNamespaceFromHost extracts preview-id and namespace from Host header.
// Expected format: diverge-tunnel-{preview-id}.{namespace}.svc.cluster.local
func extractPreviewAndNamespaceFromHost(host string) (previewID, namespace string) {
	_, previewID, namespace = extractServicePreviewAndNamespaceFromHost(host)
	return previewID, namespace
}

// extractPreviewIDFromHost extracts preview-id from Host header.
func extractPreviewIDFromHost(host string) string {
	previewID, _ := extractPreviewAndNamespaceFromHost(host)
	return previewID
}

// headersToProto converts http.Header to repeated HeaderEntry, stripping hop-by-hop and X-Diverge-* headers.
func headersToProto(h http.Header) []*pb.HeaderEntry {
	connTokens := connectionHeaderTokens(h.Values("Connection"))
	entries := make([]*pb.HeaderEntry, 0, len(h))
	for k, v := range h {
		if strings.HasPrefix(strings.ToLower(k), "x-diverge-") || isHopByHopHeader(k, connTokens) {
			continue
		}
		entries = append(entries, &pb.HeaderEntry{
			Key:    k,
			Values: v,
		})
	}
	return entries
}

func copyFilteredV2ResponseHeaders(dst http.Header, entries []*pb.HeaderEntry) {
	var connValues []string
	for _, h := range entries {
		if strings.EqualFold(h.Key, "Connection") {
			connValues = append(connValues, h.Values...)
		}
	}
	connTokens := connectionHeaderTokens(connValues)
	for _, h := range entries {
		if strings.HasPrefix(strings.ToLower(h.Key), "x-diverge-") || isHopByHopHeader(h.Key, connTokens) {
			continue
		}
		for _, v := range h.Values {
			dst.Add(h.Key, v)
		}
	}
}

// divergev1alpha1connect interface compliance
var _ divergev1alpha1connect.TunnelServiceHandler = (*TunnelManager)(nil)
