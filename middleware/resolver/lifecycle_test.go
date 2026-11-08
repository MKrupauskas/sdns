package resolver

import (
	"context"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/semihalev/sdns/config"
	"github.com/semihalev/sdns/middleware"
)

func TestResolverPipelineLifecycle(t *testing.T) {
	addr, asked, stop := startTestAuthority(t, map[string][]dns.RR{
		".": {&dns.NS{Hdr: dns.RR_Header{Name: ".", Rrtype: dns.TypeNS, Class: dns.ClassINET, Ttl: 60}, Ns: "ns.root."}},
	})
	defer stop()
	cfg := &config.Config{RootServers: []string{addr}, DNSSEC: "off", TCPKeepalive: true}
	registry := middleware.NewRegistry()
	registry.Register("resolver", func(c *config.Config) middleware.Handler { return New(c) })
	wasReady := middleware.Ready()
	p := registry.Build(cfg)
	defer p.Stop()
	h := p.Get("resolver").(*DNSHandler)
	if !h.resolver.started || h.resolver.queryer.Load() == nil || h.resolver.v4Enrich == nil {
		t.Fatal("normal pipeline did not wire and start resolver maintenance")
	}
	if middleware.Ready() != wasReady {
		t.Fatal("instance construction changed global pipeline readiness")
	}
	deadline := time.Now().Add(time.Second)
	for asked(".") == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if asked(".") == 0 {
		t.Fatal("root priming did not run without global Setup")
	}
	// An active enrichment job must be cancelled and joined by Stop.
	started, finished := make(chan struct{}), make(chan struct{})
	if !enqueueEnrich(h.resolver.v4Enrich, nsEnrichJob{owner: h.resolver, base: h.resolver.backgroundContext(), run: func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		close(finished)
	}}) {
		t.Fatal("could not enqueue enrichment")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("enrichment did not start")
	}
	p.Stop()
	p.Stop()
	select {
	case <-finished:
	default:
		t.Fatal("Stop returned before enrichment finished")
	}
	select {
	case <-h.resolver.tcpPool.cleanupDone:
	default:
		t.Fatal("TCP pool cleanup is still running")
	}
	select {
	case <-h.resolver.sfGroup.cleanupDone:
	default:
		t.Fatal("singleflight cleanup is still running")
	}
	if h.resolver.backgroundContext().Err() != context.Canceled {
		t.Fatal("maintenance was not cancelled")
	}
}

func TestResolverStopBeforeStart(t *testing.T) {
	r := NewResolver(&config.Config{})
	r.Stop()
	r.Start()
	if r.started {
		t.Fatal("a stopped resolver restarted maintenance")
	}
}
