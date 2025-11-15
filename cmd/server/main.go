package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/mat285/gateway/pkg/log"
	"github.com/mat285/gateway/pkg/server"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func cmd(ctx context.Context) *cobra.Command {
	var configPath = ""
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Start the server",
		Long:  "Start the server",
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := log.New(log.Config{
				Level: "info",
			})
			ctx = log.WithLogger(ctx, logger)
			config := server.Config{}
			if configPath != "" {
				data, err := os.ReadFile(configPath)
				if err != nil {
					return err
				}
				err = yaml.Unmarshal(data, &config)
				if err != nil {
					return err
				}
			}
			server := server.NewServer(config)
			return server.Start(ctx)
		},
	}
	cmd.PersistentFlags().StringVarP(&configPath, "config-path", "c", "", "The path to the config file")
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
