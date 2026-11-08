package resolver

import "context"

// Start begins resolver maintenance. Call it after wiring dependencies and
// restoring pipeline state. Repeated calls, including after Stop, do nothing.
// The normal middleware pipeline starts its resolver automatically.
func (r *Resolver) Start() {
	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()
	if r.started || r.stopped {
		return
	}
	r.started = true
	r.backgroundWG.Add(2)
	go func() {
		defer r.backgroundWG.Done()
		r.run()
	}()
	go func() {
		defer r.backgroundWG.Done()
		r.pruneLoop()
	}()
}

// Stop cancels and joins maintenance and active enrichment jobs, then closes
// pooled connections. Callers must stop admitting and drain client queries first.
// Shared enrichment workers remain process-wide; queued jobs for this resolver
// are skipped. Stop is safe to call concurrently and before Start.
func (r *Resolver) Stop() {
	r.stopOnce.Do(func() {
		r.lifecycleMu.Lock()
		r.stopped = true
		if r.cancelBackground != nil {
			r.cancelBackground()
		}
		r.lifecycleMu.Unlock()
		r.backgroundWG.Wait()
		if r.sfGroup != nil {
			r.sfGroup.Close()
		}
		if r.tcpPool != nil {
			r.tcpPool.Close()
		}
	})
}

func (r *Resolver) beginBackground() bool {
	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()
	if r.stopped {
		return false
	}
	r.backgroundWG.Add(1)
	return true
}

// Bare Resolver literals used by focused tests have no owned lifetime.
func (r *Resolver) backgroundContext() context.Context {
	if r.background != nil {
		return r.background
	}
	return context.Background()
}
