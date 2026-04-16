package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// config holds values loaded from ~/.tunnel/config.yaml.
type config struct {
	Server string `yaml:"server"`
	Token  string `yaml:"token"`
}

var cfg config

var (
	flagServer   string
	flagToken    string
	flagInsecure bool
)

var rootCmd = &cobra.Command{
	Use:   "lennut",
	Short: "Expose a local port to the internet",
	Long: `lennut is a reverse tunnelling tool.
It connects your local service to a public hostname
via a persistent TLS tunnel.`,

	// PersistentPreRunE runs before every subcommand.
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if err := loadConfig(cmd); err != nil {
			return err
		}
		return nil
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagServer, "server", "", `tunnel server address (e.g. tunnel.yourdomain.io:4443) Fals back to config file then "localhost:4443"`)
	rootCmd.PersistentFlags().StringVar(&flagToken, "token", "",
		`API token for authentication
Falls back to config file`)

	rootCmd.PersistentFlags().BoolVar(&flagInsecure, "insecure", false,
		"skip TLS certificate verification (local dev only)")
}

func loadConfig(cmd *cobra.Command) error {
	path, err := configFilePath()
	if err != nil {
		return err
	}

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read config: %w", err)
	}
	if err == nil {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return fmt.Errorf("parse config: %w", err)
		}
	}

	if !cmd.Flags().Changed("server") && flagServer == "" {
		if cfg.Server != "" {
			flagServer = cfg.Server
		} else {
			flagServer = "localhost:4443" // built-in default
		}
	}
	if !cmd.Flags().Changed("token") && flagToken == "" {
		flagToken = cfg.Token
	}

	return nil
}

func configFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home dir: %w", err)
	}
	dir := filepath.Join(home, ".tunnel")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create config dir: %w", err)
	}
	return filepath.Join(dir, "config.yaml"), nil
}