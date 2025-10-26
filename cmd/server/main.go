package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/mat285/gateway/pkg/log"
	"github.com/mat285/gateway/pkg/server"
	"github.com/spf13/cobra"
)

func cmd(ctx context.Context) *cobra.Command {

	cmd := &cobra.Command{
		Use:   "server",
		Short: "Start the server",
		Long:  "Start the server",
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := log.New(log.Config{
				Level: "info",
			})
			ctx = log.WithLogger(ctx, logger)
			server := server.NewServer(server.Config{
				CaddyFilePath: "_dev/Caddyfile",
			})
			return server.Start(ctx)
		},
	}
	cmd.Flags().StringP("config-path", "c", "/etc/gateway/example.yml", "The path to the config file")
	return cmd
}

func main() {
	ctx := context.Background()
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, os.Kill, syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	if err := cmd(ctx).Execute(); err != nil {
		os.Exit(1)
	}
}
