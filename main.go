package main

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/kelseyhightower/envconfig"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"
	"k8s.io/klog/v2"

	"github.com/abhinavdahiya/k8s-dra-driver-trace-collector/driver"
)

type config struct {
	NodeName    string `envconfig:"NODE_NAME" required:"true"`
	DriverName  string `envconfig:"DRIVER_NAME" default:"trace.example.com"`
	TotalShares int    `envconfig:"TOTAL_SHARES" default:"1000"`
}

func main() {
	klog.InitFlags(nil)

	// Read env
	var cfg config
	if err := envconfig.Process("", &cfg); err != nil {
		klog.ErrorS(err, "failed to read environment config")
		os.Exit(1)
	}

	// Signal context
	ctx, cancel := signal.NotifyContext(context.Background(),
		syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	// Kubernetes client
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

	// Ensure plugin data directory exists
	pluginDir := filepath.Join(kubeletplugin.KubeletPluginsDir, cfg.DriverName)
	if err := os.MkdirAll(pluginDir, 0750); err != nil {
		klog.ErrorS(err, "failed to create plugin directory", "path", pluginDir)
		os.Exit(1)
	}

	// Driver
	drv := driver.New(cfg.NodeName, cfg.DriverName, cfg.TotalShares, cancel)

	// Start kubelet plugin
	helper, err := kubeletplugin.Start(ctx, drv,
		kubeletplugin.DriverName(cfg.DriverName),
		kubeletplugin.KubeClient(clientset),
		kubeletplugin.NodeName(cfg.NodeName),
	)
	if err != nil {
		klog.ErrorS(err, "failed to start kubelet plugin")
		os.Exit(1)
	}

	// Publish resource inventory
	resources := drv.BuildResources()
	if err := helper.PublishResources(ctx, resources); err != nil {
		klog.ErrorS(err, "failed to publish resources")
		os.Exit(1)
	}

	klog.InfoS("driver started successfully",
		"nodeName", cfg.NodeName,
		"driverName", cfg.DriverName,
		"totalShares", cfg.TotalShares)

	// Wait for shutdown
	<-ctx.Done()
	klog.InfoS("shutting down")
	helper.Stop()
}
