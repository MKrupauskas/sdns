---
layout: doc
title: Middleware
category: Development
order: 2
description: The handler contract, the chain, and where a new middleware goes in it.
---

Every query is served by a chain of middlewares. Each is a `Handler`:

```go
type Handler interface {
    // Name must match the name used in Register so Pipeline.Get
    // can resolve the handler back.
    Name() string

    // ServeDNS processes a query. Call ch.Next to continue the chain,
    // or ch.Cancel / ch.CancelWithRcode to stop it.
    ServeDNS(ctx context.Context, ch *Chain)
}
```

A middleware either answers the query and stops the chain, or modifies
something and continues it:

```go
ch.Next(ctx)                    // continue
ch.Cancel()                     // stop, writing nothing further
ch.CancelWithRcode(rcode, do)   // write a reply with this rcode, then stop
```

Only `Next` takes the context. `do` on `CancelWithRcode` sets the DO bit on the
response's OPT record. It is not an authoritative flag.

## The default chain

```
recovery → metrics → dnstap → accesslist → ratelimit → reflex → edns
  → accesslog → chaos → ddr → hostsfile → views → blocklist → rpz → as112
  → kubernetes → dns64 → cache → failover → resolver → forwarder
```

The order lives in `middleware/defaults`, which is generated. It is a package
rather than an `init` in `main` specifically so that everything needing the real
chain can ask for it: the binary, the benchmarks, a test harness. When the list
lived in `main` each of those kept its own copy, and they drifted; a benchmark
quietly measuring five handlers fewer than production still reports a number.

`RegisterUpTo(name)` registers the chain up to but not including `name`, which
is how a harness replaces the tail with a stub while keeping everything ahead of
it real.

## Adding one to the chain

An in-tree middleware lives in `middleware/<name>`, as a package of that name
with a `New(cfg *config.Config)` constructor. Add the name to `middlewareList`
in `gen.go` at the position it belongs, then regenerate:

```bash
go generate
```

That rewrites `middleware/defaults`, so the binary, the benchmarks and every
harness pick up the new chain together.

Do not register from an `init` function. The registry starts empty, and the
default chain is registered only when the server sets up, after every `init`
has run: a `Register` call in `init` lands at index 0, ahead of `recovery` and
access control, and `RegisterBefore(name, ctor, "cache")` panics because
`cache` is not registered yet. The placement calls are for code that runs after
the default chain is in place, which is how the plugin loader uses them:

- `Register(name, ctor)` appends to the end.
- `RegisterAt(name, ctor, idx)`, at an index; out of range panics.
- `RegisterBefore(name, ctor, before)`, immediately before a named middleware;
  panics if the target is not registered.

Registering a name twice panics. That is intentional: a duplicate name means
`Pipeline.Get` cannot resolve the handler, and failing at startup is better than
resolving to the wrong one.

## Where to put a new middleware

Position is a design decision, not a detail:

- **Before `cache`** if it must see queries the cache would otherwise answer.
  Policy and filtering belong here. This is where dynamic plugins are
  inserted. Note that this is not "every query": everything listed ahead of
  `cache` in the default chain (access control, rate limiting, reflex, edns,
  chaos, DDR, the hosts file, views, the blocklist, RPZ, AS112, kubernetes and
  DNS64) runs earlier, and any of them can end the chain first.
- **After `cache`** if it only concerns queries that actually need resolving.
- **Before `accesslist`** essentially never; nothing should run ahead of access
  control except recovery and instrumentation.

## Self-declaring middleware

A middleware that needs wiring beyond construction declares it through a marker
or setter interface that `middleware.Setup` looks for, rather than by adding a
branch to the server's startup code. Keeping the knowledge in the middleware is
what lets the chain stay a list.

## Testing one

Build a chain with just your handler and a stub behind it, feed it a
`*dns.Msg`, and assert on what came back. `middleware.HandlerFunc` adapts a
plain function into a `Handler`, which is enough for the stub.

No assertion library and no network; see
[Building and testing]({{ '/docs/development/building/' | relative_url }}).

## Owning an independent pipeline

Embedders use the same `config.Config`, middleware constructors, and
`Registry.Build` as the server. `Build` wires the internal queryers and stores,
restores saved state, then calls `Starter.Start` on enabled handlers. It does
not publish the pipeline globally or depend on `middleware.Ready()`.

A resolver constructed with `resolver.New(cfg)` keeps its normal priming,
trust-anchor upkeep, pruning, and enrichment behavior. Its maintenance begins
when the owning pipeline starts it. Direct users of `NewResolver` call `Start`
after wiring dependencies. Constructors no longer start resolver network
maintenance against a partially built pipeline.

To execute queries in memory, use `middleware.NewPipelineQueryer(pipeline)`.
Callers own their request messages; copy a message first if another component
still needs it. Include the cache and EDNS middlewares when you need CNAME
completion, caching, DNSSEC response policy, and EDNS handling. The host owns its
listeners and client admission; it must enforce its final transport size limit.

At shutdown, stop accepting queries and cancel or drain active calls, then call
`pipeline.Stop()`. It stops handlers once in reverse order. The resolver cancels
and joins maintenance, active enrichment, singleflight leaders and upstream
attempts, and closes its cleanup loops and pooled TCP connections. Shared
bounded enrichment workers remain process-wide; they skip queued jobs owned by
a stopped resolver. `pipeline.Persist(ctx)` saves the instance's state after
shutdown without referring to the global pipeline. Do not stop a sub-pipeline:
its handlers belong to the original owning pipeline.

A pipeline and its handler instances have one lifetime. A reload constructs a
new pipeline from ordinary configuration; a stopped instance cannot restart.
Independent instances may coexist, including during a host's configuration
reload. Give independently managed persistent instances separate state
directories.
