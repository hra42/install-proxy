package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/logger"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/spf13/cobra"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "install-proxy",
		Short:         "Serve install scripts for many tools from one binary, routed by Host header.",
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          serve,
	}
	cmd.PersistentFlags().StringP("config", "c", "/etc/install-proxy/config.yml", "path to YAML config file")
	cmd.AddCommand(validateCmd(), versionCmd(), healthcheckCmd())
	return cmd
}

func validateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Load and validate the config file without starting the server.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := cmd.Flags().GetString("config")
			if err != nil {
				return err
			}
			cfg, source, err := Resolve(path, cmd.Flags().Changed("config"))
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "ok: %s — %d tools, listen %s\n", source, len(cfg.Tools), cfg.Listen)
			return nil
		},
	}
}

func healthcheckCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "healthcheck",
		Short: "Probe the local /healthz endpoint and exit non-zero on failure.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			addr, err := cmd.Flags().GetString("addr")
			if err != nil {
				return err
			}
			client := &http.Client{Timeout: 3 * time.Second}
			resp, err := client.Get("http://" + addr + "/healthz")
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("healthz returned %d", resp.StatusCode)
			}
			return nil
		},
	}
	cmd.Flags().String("addr", "127.0.0.1:8080", "address to probe")
	return cmd
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information.",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "install-proxy %s (commit %s, built %s)\n", version, commit, date)
		},
	}
}

func serve(cmd *cobra.Command, _ []string) error {
	path, err := cmd.Flags().GetString("config")
	if err != nil {
		return err
	}

	cfg, source, err := Resolve(path, cmd.Flags().Changed("config"))
	if err != nil {
		return err
	}

	app := fiber.New(fiber.Config{
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	})

	app.Use(recover.New())
	app.Use(logger.New())

	Register(app, cfg)

	errCh := make(chan error, 1)
	go func() {
		if err := app.Listen(cfg.Listen, fiber.ListenConfig{DisableStartupMessage: true}); err != nil {
			errCh <- err
		}
		close(errCh)
	}()

	log.Printf("listening on %s, %d tools configured from %s", cfg.Listen, len(cfg.Tools), source)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("listen: %w", err)
		}
		return nil
	case sig := <-sigCh:
		log.Printf("received %s, shutting down", sig)
		if err := app.ShutdownWithTimeout(5 * time.Second); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	}
}
