package main

import (
	"os"

	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:   "kube-nat",
		Short: "Kubernetes-native NAT for AWS",
	}
	root.AddCommand(agentCmd())
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func agentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "agent",
		Short: "Run the NAT agent (DaemonSet mode)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgent()
		},
	}
}

func runAgent() error {
	// wired up in Task 12
	return nil
}
