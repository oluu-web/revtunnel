// cmd/agent/config.go
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage tunnel configuration",
}

var setTokenCmd = &cobra.Command{
	Use:   "set-token <token>",
	Short: "Save an API token to the config file",
	Long: `Saves your API token to ~/.tunnel/config.yaml.
After running this once, you never need to pass --token again.
Example: tunnel config set-token secret123`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return writeToken(args[0])
	},
}

var setServerCmd = &cobra.Command{
	Use:   "set-server <address>",
	Short: "Save the tunnel server address to the config file",
	Long: `Saves the tunnel server address to ~/.tunnel/config.yaml.
After running this once, you never need to pass --server again.
Example:
  tunnel config set-server tunnel.yourdomain.io:4443`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return writeServer(args[0])
	},
}

// setTunnelIDCmd persists the tunnel ID returned by POST /v1/tunnels.
// This is a temporary helper used until chunk 4 wires up automatic
// tunnel registration before the HELLO handshake.
var setTunnelIDCmd = &cobra.Command{
	Use:   "set-tunnel-id <id>",
	Short: "Save a tunnel ID to the config file",
	Long: `Saves a tunnel ID to ~/.tunnel/config.yaml.
The tunnel ID is returned by the server when you register a tunnel
via POST /v1/tunnels. This command is a temporary helper until
tunnel registration is automated in the agent startup flow.
Example:
  tunnel config set-tunnel-id 550e8400-e29b-41d4-a716-446655440000`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return writeTunnelID(args[0])
	},
}

func init() {
	configCmd.AddCommand(setTokenCmd)
	configCmd.AddCommand(setServerCmd)
	configCmd.AddCommand(setTunnelIDCmd)

	rootCmd.AddCommand(configCmd)
}

func writeToken(token string) error {
	return updateConfig(func(c *config) {
		c.Token = token
	})
}

func writeServer(server string) error {
	return updateConfig(func(c *config) {
		c.Server = server
	})
}

func writeTunnelID(id string) error {
	return updateConfig(func(c *config) {
		c.TunnelID = id
	})
}

func updateConfig(fn func(*config)) error {
	path, err := configFilePath()
	if err != nil {
		return err
	}

	var c config
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read config: %w", err)
	}
	if err == nil {
		if err := yaml.Unmarshal(data, &c); err != nil {
			return fmt.Errorf("parse config: %w", err)
		}
	}

	fn(&c)

	out, err := yaml.Marshal(&c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(path, out, 0600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	fmt.Printf("config saved → %s\n", path)
	return nil
}