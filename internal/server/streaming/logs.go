package streaming

import (
	"context"
	"io"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// LogStreamer streams logs from pods.
type LogStreamer struct {
	clientset kubernetes.Interface
}

func NewLogStreamer(clientset kubernetes.Interface) *LogStreamer {
	return &LogStreamer{clientset: clientset}
}

// StreamPodLogs streams logs from a specific pod/container.
func (l *LogStreamer) StreamPodLogs(ctx context.Context, namespace, podName, container string, follow bool, tailLines int64, since *time.Time) (io.ReadCloser, error) {
	opts := &corev1.PodLogOptions{
		Container:  container,
		Follow:     follow,
		Timestamps: true,
	}
	if tailLines > 0 {
		opts.TailLines = &tailLines
	}
	if since != nil {
		sinceTime := metav1.NewTime(*since)
		opts.SinceTime = &sinceTime
	}

	req := l.clientset.CoreV1().Pods(namespace).GetLogs(podName, opts)
	return req.Stream(ctx)
}
