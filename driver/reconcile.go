package driver

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"

	"github.com/abhinavdahiya/k8s-dra-driver-trace-collector/driver/alloy"
	"github.com/abhinavdahiya/k8s-dra-driver-trace-collector/driver/cdi"
)

// desiredState is a snapshot of what the reconciler should converge toward.
type desiredState struct {
	claims map[string]*PreparedClaim // keyed by claim UID string
}

// Reconcile converges filesystem and Alloy state toward the desired
// state expressed by d.prepared. Every step is idempotent. On any
// failure the reconciler logs the error and returns — the next
// trigger or timer tick retries from the top.
func (d *Driver) Reconcile(ctx context.Context) error {
	// 1. Snapshot desired state under lock.
	desired := d.snapshotDesired()

	// 2. Scan filesystem for actual state.
	actualConfigs := scanGlob(d.cfg.Alloy.ConfigDir, "claim-*.alloy", "claim-", ".alloy")
	actualCDI := scanGlob(d.cdiDir, "trace-*.json", "trace-", ".json")
	actualSocketDirs := scanDirs(d.cfg.Alloy.SocketDir)

	dirty := false // tracks whether Alloy reload is needed

	// 3. Ensure desired claims have correct config files and CDI specs.
	for uid, pc := range desired.claims {
		// Config file: render expected content and compare.
		params := alloy.ComputeParams(uid, pc.TotalShares(), d.cfg)
		expected, err := alloy.RenderConfig(params)
		if err != nil {
			klog.ErrorS(err, "failed to render config", "claimUID", uid)
			continue
		}

		configPath := filepath.Join(d.cfg.Alloy.ConfigDir, alloy.ConfigFileName(uid))
		actual, readErr := os.ReadFile(configPath)
		if readErr != nil || !bytes.Equal(actual, expected) {
			if readErr != nil {
				klog.V(2).InfoS("writing missing config file", "claimUID", uid)
			} else {
				klog.V(2).InfoS("overwriting stale config file", "claimUID", uid)
			}
			if err := alloy.WriteConfigFile(d.cfg.Alloy.ConfigDir, uid, expected); err != nil {
				klog.ErrorS(err, "failed to write config file", "claimUID", uid)
				continue
			}
			dirty = true
		}

		// CDI spec: ensure it exists.
		cdiPath := filepath.Join(d.cdiDir, "trace-"+uid+".json")
		if _, err := os.Stat(cdiPath); os.IsNotExist(err) {
			klog.V(2).InfoS("writing missing CDI spec", "claimUID", uid)
			if err := cdi.WriteSpec(d.cdiDir, uid, params.SocketPath); err != nil {
				klog.ErrorS(err, "failed to write CDI spec", "claimUID", uid)
			}
		}

		// Socket directory: ensure it exists.
		sockDir := filepath.Dir(params.SocketPath)
		if err := os.MkdirAll(sockDir, 0755); err != nil {
			klog.ErrorS(err, "failed to create socket directory", "claimUID", uid, "dir", sockDir)
		}
	}

	// 4. Remove orphan files (on disk but not in desired state).
	for uid := range actualConfigs {
		if _, ok := desired.claims[uid]; !ok {
			klog.V(2).InfoS("deleting orphan config file", "claimUID", uid)
			if err := alloy.DeleteConfigFile(d.cfg.Alloy.ConfigDir, uid); err != nil {
				klog.ErrorS(err, "failed to delete orphan config", "claimUID", uid)
			} else {
				dirty = true
			}
		}
	}
	for uid := range actualCDI {
		if _, ok := desired.claims[uid]; !ok {
			klog.V(2).InfoS("deleting orphan CDI spec", "claimUID", uid)
			if err := cdi.DeleteSpec(d.cdiDir, uid); err != nil {
				klog.ErrorS(err, "failed to delete orphan CDI spec", "claimUID", uid)
			}
		}
	}
	for uid := range actualSocketDirs {
		if _, ok := desired.claims[uid]; !ok {
			klog.V(2).InfoS("deleting orphan socket directory", "claimUID", uid)
			dirPath := filepath.Join(d.cfg.Alloy.SocketDir, uid)
			if err := os.RemoveAll(dirPath); err != nil {
				klog.ErrorS(err, "failed to delete orphan socket directory", "claimUID", uid)
			}
		}
	}

	// 5. Single Alloy reload if any config files changed.
	if dirty && d.reloader != nil {
		klog.V(2).InfoS("triggering Alloy config reload")
		if err := d.reloader.Reload(ctx); err != nil {
			klog.ErrorS(err, "Alloy reload failed, will retry next cycle")
			return err
		}
		klog.V(2).InfoS("Alloy config reload succeeded")
	}

	return nil
}

// StartReconciler starts the reconciler goroutine. It processes
// items from the workqueue (triggered by Prepare/Unprepare) and
// also runs on a periodic timer as a safety net.
func (d *Driver) StartReconciler(ctx context.Context) {
	// Periodic timer goroutine: enqueues reconcileKey on each tick.
	go func() {
		ticker := time.NewTicker(d.cfg.Reconciler.Interval.Duration)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				d.queue.Add(reconcileKey)
			}
		}
	}()

	// Worker goroutine: processes items from the workqueue.
	go func() {
		klog.InfoS("reconciler started", "interval", d.cfg.Reconciler.Interval.Duration)
		defer klog.InfoS("reconciler stopped")

		for {
			key, shutdown := d.queue.Get()
			if shutdown {
				return
			}
			klog.V(4).InfoS("reconciler processing", "key", key)
			if err := d.Reconcile(ctx); err != nil {
				klog.ErrorS(err, "reconcile failed, will retry")
			}
			d.queue.Done(key)
		}
	}()

	// Shutdown the queue when context is cancelled.
	go func() {
		<-ctx.Done()
		d.queue.ShutDown()
	}()
}

// StartAdminConfigSyncer starts a separate reconciler goroutine that
// periodically syncs admin *.alloy files from adminConfigDir into
// configDir. This is intentionally NOT part of the claim Reconcile()
// method — it runs on its own timer. If any files changed, it
// triggers an Alloy reload.
func (d *Driver) StartAdminConfigSyncer(ctx context.Context) {
	if d.cfg.Alloy.AdminConfigDir == "" {
		klog.InfoS("admin config sync disabled (adminConfigDir not set)")
		return
	}

	go func() {
		klog.InfoS("admin config syncer started",
			"src", d.cfg.Alloy.AdminConfigDir, "dst", d.cfg.Alloy.ConfigDir,
			"interval", d.cfg.Reconciler.Interval.Duration)
		defer klog.InfoS("admin config syncer stopped")

		ticker := time.NewTicker(d.cfg.Reconciler.Interval.Duration)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				changed, err := alloy.SyncAdminConfig(d.cfg.Alloy.AdminConfigDir, d.cfg.Alloy.ConfigDir)
				if err != nil {
					klog.ErrorS(err, "admin config sync failed, will retry",
						"src", d.cfg.Alloy.AdminConfigDir, "dst", d.cfg.Alloy.ConfigDir)
					continue
				}
				if changed && d.reloader != nil {
					klog.V(2).InfoS("admin config changed, triggering Alloy reload")
					if err := d.reloader.Reload(ctx); err != nil {
						klog.ErrorS(err, "Alloy reload after admin config sync failed, will retry")
					}
				}
			}
		}
	}()
}

// snapshotDesired returns a copy of d.prepared under lock.
func (d *Driver) snapshotDesired() desiredState {
	d.mu.Lock()
	defer d.mu.Unlock()

	claims := make(map[string]*PreparedClaim, len(d.prepared))
	for uid, pc := range d.prepared {
		claims[string(uid)] = pc
	}
	return desiredState{claims: claims}
}

// scanDirs scans a directory for subdirectories and returns a set
// of their names. Used to discover per-claim socket directories.
func scanDirs(dir string) map[string]struct{} {
	result := make(map[string]struct{})
	entries, err := os.ReadDir(dir)
	if err != nil {
		klog.V(4).InfoS("readdir failed", "dir", dir, "err", err)
		return result
	}
	for _, e := range entries {
		if e.IsDir() {
			result[e.Name()] = struct{}{}
		}
	}
	return result
}

// scanGlob scans a directory for files matching the pattern and
// extracts claim UIDs by stripping the prefix and suffix.
// Returns a set of claim UID strings.
func scanGlob(dir, pattern, prefix, suffix string) map[string]struct{} {
	result := make(map[string]struct{})
	matches, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil {
		klog.V(4).InfoS("glob failed", "dir", dir, "pattern", pattern, "err", err)
		return result
	}
	for _, m := range matches {
		base := filepath.Base(m)
		uid := strings.TrimPrefix(base, prefix)
		uid = strings.TrimSuffix(uid, suffix)
		if uid != "" {
			result[uid] = struct{}{}
		}
	}
	return result
}

// PreparedClaims returns a copy of the prepared claims map.
// Used by tests and startup recovery.
func (d *Driver) PreparedClaims() map[types.UID]*PreparedClaim {
	d.mu.Lock()
	defer d.mu.Unlock()

	result := make(map[types.UID]*PreparedClaim, len(d.prepared))
	for uid, pc := range d.prepared {
		result[uid] = pc
	}
	return result
}
