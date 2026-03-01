package main

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"
	"k8s.io/klog/v2"

	"github.com/abhinavdahiya/k8s-dra-driver-trace-collector/driver"
	"github.com/abhinavdahiya/k8s-dra-driver-trace-collector/driver/alloy"
	"github.com/abhinavdahiya/k8s-dra-driver-trace-collector/driver/config"
)

const (
	defaultConfigFile = "/etc/trace-dra-driver/config.yaml"
)

func main() {
	klog.InitFlags(nil)

	// NODE_NAME from Downward API (required).
	nodeName := os.Getenv("NODE_NAME")
	if nodeName == "" {
		klog.ErrorS(nil, "NODE_NAME environment variable is required")
		os.Exit(1)
	}

	// CONFIG_FILE path (optional, defaults to /etc/trace-dra-driver/config.yaml).
	configFile := os.Getenv("CONFIG_FILE")
	if configFile == "" {
		configFile = defaultConfigFile
	}

	// Load config.
	cfg, err := config.LoadConfigFile(configFile)
	if err != nil {
		klog.ErrorS(err, "failed to load config file", "path", configFile)
		os.Exit(1)
	}

	// If adminConfigDir is set (Variant B: admin config from ConfigMap),
	// do an initial sync of *.alloy files into the writable configDir
	// before Alloy reads them. The continuous sync reconciler handles
	// subsequent changes.
	if cfg.Alloy.AdminConfigDir != "" {
		changed, err := alloy.SyncAdminConfig(cfg.Alloy.AdminConfigDir, cfg.Alloy.ConfigDir)
		if err != nil {
			klog.ErrorS(err, "failed to sync admin config on startup",
				"src", cfg.Alloy.AdminConfigDir, "dst", cfg.Alloy.ConfigDir)
			os.Exit(1)
		}
		klog.InfoS("initial admin config sync", "changed", changed,
			"src", cfg.Alloy.AdminConfigDir, "dst", cfg.Alloy.ConfigDir)
	}

	// Signal context.
	ctx, cancel := signal.NotifyContext(context.Background(),
		syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	// Kubernetes client.
	restCfg, err := rest.InClusterConfig()
	if err != nil {
		klog.ErrorS(err, "failed to get in-cluster config")
		os.Exit(1)
	}
	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		klog.ErrorS(err, "failed to create kubernetes clientset")
		os.Exit(1)
	}

	// Ensure plugin data directory exists.
	pluginDir := filepath.Join(kubeletplugin.KubeletPluginsDir, cfg.Driver.Name)
	if err := os.MkdirAll(pluginDir, 0750); err != nil {
		klog.ErrorS(err, "failed to create plugin directory", "path", pluginDir)
		os.Exit(1)
	}

	// Ensure checkpoint directory exists.
	if err := os.MkdirAll(cfg.Driver.CheckpointDir, 0750); err != nil {
		klog.ErrorS(err, "failed to create checkpoint directory", "path", cfg.Driver.CheckpointDir)
		os.Exit(1)
	}

	// Alloy admin API client for config reloads.
	alloyClient := alloy.NewClient(cfg.Alloy.Address)

	// Driver.
	drv := driver.New(driver.DriverOptions{
		NodeName:   nodeName,
		Config:     cfg,
		CancelFunc: cancel,
		Reloader:   alloyClient,
	})

	// Load checkpoint: recover prepared claims from previous run.
	// Non-fatal — if the checkpoint is missing or corrupt, the driver
	// starts fresh and kubelet re-calls Prepare for active claims.
	if n, err := drv.LoadCheckpoint(); err != nil {
		klog.ErrorS(err, "failed to load checkpoint, starting fresh")
	} else if n > 0 {
		klog.InfoS("loaded checkpoint", "claims", n)
	}

	// Synchronous startup reconcile: converge filesystem state before
	// accepting kubelet gRPC calls. Errors are logged but not fatal
	// (the periodic reconciler will retry).
	if err := drv.Reconcile(ctx); err != nil {
		klog.ErrorS(err, "startup reconcile failed, will retry asynchronously")
	}

	// Start kubelet plugin.
	helper, err := kubeletplugin.Start(ctx, drv,
		kubeletplugin.DriverName(cfg.Driver.Name),
		kubeletplugin.KubeClient(clientset),
		kubeletplugin.NodeName(nodeName),
	)
	if err != nil {
		klog.ErrorS(err, "failed to start kubelet plugin")
		os.Exit(1)
	}

	// Publish resource inventory.
	resources := drv.BuildResources()
	if err := helper.PublishResources(ctx, resources); err != nil {
		klog.ErrorS(err, "failed to publish resources")
		os.Exit(1)
	}

	klog.InfoS("driver started successfully",
		"nodeName", nodeName,
		"driverName", cfg.Driver.Name,
		"totalShares", cfg.Driver.TotalShares,
		"configFile", configFile)

	// Start async reconciler goroutine (periodic + trigger-based).
	drv.StartReconciler(ctx)

	// Start separate admin config sync reconciler (continuous sync
	// from ConfigMap-mounted adminConfigDir into configDir).
	drv.StartAdminConfigSyncer(ctx)

	// Wait for shutdown.
	<-ctx.Done()
	klog.InfoS("shutting down")
	helper.Stop()
}
