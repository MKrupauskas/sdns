package resolver

import (
	"context"
	"net"
	"testing"

	"github.com/miekg/dns"
	"github.com/semihalev/sdns/config"
	"github.com/semihalev/sdns/middleware"
)

func TestEmbeddedReferralWalk(t *testing.T) {
	n := newHermeticNet(t)
	// Omit glue for this delegation to force an internal nameserver lookup
	// through the application-owned Queryer, as well as the referral walk.
	z := n.DelegateVia("shop.test.", "ns.infra.test.")
	infra := n.Delegate("infra.test.")
	infra.Serve(mustRR(t, "ns.infra.test. 300 IN A "+z.glue.String()))
	z.Serve(mustRR(t, "www.shop.test. 300 IN A 192.0.2.42"))
	cfg := n.Config()
	// Exercise the same unsigned configuration used by the CoreDNS POC.
	cfg.DNSSEC = "off"
	cfg.IPv6Access = false
	h := NewEmbedded(cfg)
	t.Cleanup(h.Stop)
	if h.resolver.v4Enrich != nil || h.resolver.v6Enrich != nil || h.resolver.refreshLane != nil {
		t.Fatal("embedded resolver started enrichment pools")
	}
	byGlue := make(map[string]string)
	for _, zone := range n.zones {
		byGlue[net.JoinHostPort(zone.glue.String(), "53")] = zone.server.addr
		byGlue[net.JoinHostPort(zone.glue6.String(), "53")] = zone.server.addr
	}
	mapper := func(addr string) string {
		if target, ok := byGlue[addr]; ok {
			return target
		}
		return addr
	}
	h.resolver.resolveTarget.Store(&mapper)
	wasReady := middleware.Ready()
	registry := middleware.NewRegistry()
	registry.Register(h.Name(), func(*config.Config) middleware.Handler { return h })
	q := middleware.NewPipelineQueryer(registry.Build(cfg))
	h.SetQueryer(q)
	h.SetPrefetchQueryer(q)
	req := new(dns.Msg).SetQuestion("www.shop.test.", dns.TypeA)
	resp, err := q.Query(context.Background(), req)
	if err != nil || resp == nil || resp.Rcode != dns.RcodeSuccess {
		t.Fatalf("response=%v error=%v", resp, err)
	}
	found := false
	for _, rr := range resp.Answer {
		if a, ok := rr.(*dns.A); ok && a.A.String() == "192.0.2.42" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no A answer: %v", resp)
	}
	if infra.server.asked("ns.infra.test.", dns.TypeA) == 0 {
		t.Fatal("the internal nameserver lookup did not reach the authority")
	}
	if middleware.Ready() != wasReady {
		t.Fatal("embedding changed global pipeline readiness")
	}
}
