package middleware

import (
	"context"
	"reflect"
	"testing"

	"github.com/semihalev/sdns/config"
)

type lifecycleHandler struct{ events *[]string }

func (h *lifecycleHandler) Name() string                     { return "lifecycle" }
func (h *lifecycleHandler) ServeDNS(context.Context, *Chain) {}
func (h *lifecycleHandler) SetQueryer(Queryer)               { *h.events = append(*h.events, "wire") }
func (h *lifecycleHandler) Restore()                         { *h.events = append(*h.events, "restore") }
func (h *lifecycleHandler) Start()                           { *h.events = append(*h.events, "start") }
func (h *lifecycleHandler) Stop()                            { *h.events = append(*h.events, "stop") }

func TestRegistryLifecycle(t *testing.T) {
	var events []string
	r := NewRegistry()
	r.Register("lifecycle", func(*config.Config) Handler { return &lifecycleHandler{&events} })
	p := r.Build(&config.Config{})
	p.Stop()
	p.Stop()
	if want := []string{"wire", "restore", "start", "stop"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("lifecycle = %v, want %v", events, want)
	}
}
