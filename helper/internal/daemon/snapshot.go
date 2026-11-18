package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"
)

// Snapshot runs `ptb-helper snapshot --json`: one full refresh (usage, limits, companion) with no
// events, then one state line on out. It takes the state-dir lock like serve, because it writes
// the save. It never evaluates or persists the limit-alert tiers (that would swallow the next
// alert) and reads sprites from disk only. budget bounds the companion data loading.
func Snapshot(cfg Config, out io.Writer, budget time.Duration) int {
	cfg = cfg.withDefaults()
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		cfg.Logf("cannot create the state dir: %v", err)
		return ExitStateDir
	}
	lock, err := acquireLock(cfg.StateDir, cfg.LockWait)
	if err != nil {
		cfg.Logf("%v", err)
		var running errAlreadyRunning
		if errors.As(err, &running) {
			return ExitRunning
		}
		return ExitStateDir
	}
	defer lock.Close()
	d, err := open(cfg, nil, true)
	if err != nil {
		cfg.Logf("%v", err)
		return ExitStateDir
	}
	defer d.cancel()
	ctx, cancel := context.WithTimeout(d.ctx, budget)
	defer cancel()

	now := cfg.Now().In(d.loc)
	results := make([]scanResult, len(cfg.Providers))
	for i, s := range cfg.Providers {
		results[i].enr, results[i].err = s.Scan(now, d.settings.weekday())
	}
	d.applyUsage(now, results)
	d.companionSync(ctx)

	var fetched []fetchResult
	for _, spec := range cfg.Limits {
		st, err := spec.Fetcher.Fetch(ctx)
		fetched = append(fetched, fetchResult{providerID: spec.ProviderID, status: st, err: err})
	}
	d.applyLimits(cfg.Now(), fetched)
	d.grantCandies()

	d.refreshing, d.fromCache = false, false
	d.saveSnapshot(true)
	for _, src := range cfg.Providers {
		if err := src.Flush(); err != nil {
			cfg.Logf("flush %s usage cache: %v", src.ID(), err)
		}
	}
	b, err := json.Marshal(d.buildState(cfg.Now()))
	if err != nil {
		cfg.Logf("encode state: %v", err)
		return ExitInternal
	}
	if _, err := out.Write(append(b, '\n')); err != nil {
		cfg.Logf("write state: %v", err)
		return ExitInternal
	}
	return ExitOK
}

// companionSync runs Update and warms what it missed until nothing is missing, a warm makes no
// progress or ctx ends.
func (d *daemon) companionSync(ctx context.Context) {
	for pass := 0; pass < maxWarmPasses && ctx.Err() == nil; pass++ {
		_, wants := d.updateCompanion()
		if len(wants) == 0 {
			return
		}
		if got, _ := d.data.warm(ctx, wants); got == 0 {
			return
		}
	}
}
