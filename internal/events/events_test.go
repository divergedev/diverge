package events

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/runtime"
)

type fakeEventRecorder struct {
	called    bool
	eventtype string
	reason    string
	action    string
	note      string
	args      []interface{}
}

func (f *fakeEventRecorder) Eventf(regarding runtime.Object, related runtime.Object, eventtype, reason, action, note string, args ...interface{}) {
	f.called = true
	f.eventtype = eventtype
	f.reason = reason
	f.action = action
	f.note = note
	f.args = args
}

func TestRecorder_Event(t *testing.T) {
	fakeEr := &fakeEventRecorder{}
	recorder := NewRecorder(fakeEr)

	recorder.Event(nil, "Normal", "TestReason", "TestMessage")

	assert.True(t, fakeEr.called)
	assert.Equal(t, "Normal", fakeEr.eventtype)
	assert.Equal(t, "TestReason", fakeEr.reason)
	assert.Equal(t, "TestReason", fakeEr.action)
	assert.Equal(t, "TestMessage", fakeEr.note)
	assert.Empty(t, fakeEr.args)
}

func TestRecorder_Eventf(t *testing.T) {
	fakeEr := &fakeEventRecorder{}
	recorder := NewRecorder(fakeEr)

	recorder.Eventf(nil, "Warning", "ErrorReason", "Error: %v", "bad")

	assert.True(t, fakeEr.called)
	assert.Equal(t, "Warning", fakeEr.eventtype)
	assert.Equal(t, "ErrorReason", fakeEr.reason)
	assert.Equal(t, "ErrorReason", fakeEr.action)
	assert.Equal(t, "Error: %v", fakeEr.note)
	assert.Equal(t, []interface{}{"bad"}, fakeEr.args)
}
