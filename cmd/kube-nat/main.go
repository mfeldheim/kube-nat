package main

import (
	"fmt"
	"os"

	"github.com/kube-nat/kube-nat/internal/agent"
	"github.com/kube-nat/kube-nat/internal/config"
	"github.com/kube-nat/kube-nat/internal/dashboard"
	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func main() {
	root := &cobra.Command{
		Use:   "kube-nat",
		Short: "Kubernetes-native NAT for AWS",
	}
	root.AddCommand(agentCmd())
	root.AddCommand(dashboardCmd())
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func agentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "agent",
		Short: "Run the NAT agent (DaemonSet mode)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			return agent.Run(cfg)
		},
	}
}

func dashboardCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "dashboard",
		Short: "Run the real-time NAT dashboard",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			k8sCfg, err := rest.InClusterConfig()
			if err != nil {
				return fmt.Errorf("k8s in-cluster config: %w", err)
			}
			k8sClient, err := kubernetes.NewForConfig(k8sCfg)
			if err != nil {
				return fmt.Errorf("k8s client: %w", err)
			}
			srv := dashboard.NewServer(dashboard.Config{
				K8sClient:      k8sClient,
				Namespace:      cfg.Namespace,
				MetricsPort:    cfg.MetricsPort,
				ScrapeInterval: int(cfg.ScrapeInterval.Seconds()),
			})
			return srv.Run(cmd.Context(), fmt.Sprintf(":%d", cfg.DashboardPort))
		},
	}
}
