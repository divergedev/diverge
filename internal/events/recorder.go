package events

import (
	"k8s.io/apimachinery/pkg/runtime"
	k8sevents "k8s.io/client-go/tools/events"
)

// Recorder wraps events.EventRecorder with convenience methods
// matching the old record.EventRecorder API for easy migration.
type Recorder struct {
	k8sevents.EventRecorder
}

// NewRecorder wraps an events.EventRecorder.
func NewRecorder(er k8sevents.EventRecorder) *Recorder {
	return &Recorder{EventRecorder: er}
}

// Event emits an event for the given object.
func (r *Recorder) Event(object runtime.Object, eventtype, reason, message string) {
	r.EventRecorder.Eventf(object, nil, eventtype, reason, reason, message)
}

// Eventf emits a formatted event for the given object.
func (r *Recorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
	r.EventRecorder.Eventf(object, nil, eventtype, reason, reason, messageFmt, args...)
}
