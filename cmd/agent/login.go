package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/spf13/cobra"
)

var loginCmd = &cobra.Command{
	Use: "login",
	Short: "Authenticate with an API keey and save a session token",
	Long: `Exchanges your API key for a short-lived session token and saves it to ~/.tunnel/config.yaml. After running this once, tunnel http will use the saved token automatically until it expires.
	Example:
 	tunnel login --api-key lntliveab12cd34.yoursecrethere`,
		RunE: func(cmd *cobra.Command, args []string) error {
			apiKey, _ := cmd.Flags().GetString("api-key")
			if apiKey == "" {
				return fmt.Errorf("--api-key is required")
			}
			return runLogin(apiKey)
		},
}

func init() {
	loginCmd.Flags().String("api-key", "", "API key issues by the tunnel server (required)")
	_ = loginCmd.MarkFlagRequired("api-key")
	rootCmd.AddCommand(loginCmd)
}

func runLogin(apiKey string) error {
	token, ttl, err := fetchToken(apiKey)
	if err != nil {
		return err
	}
	if err := writeToken(token); err != nil {
		return fmt.Errorf("save token: %w", err)
	}
	if err := writeAPIKey(apiKey); err != nil {
		return fmt.Errorf("save api key: %w", err)
	}
	flagToken = token
	flagAPIKey = apiKey
	fmt.Printf("logged in — token saved (expires in %d minutes)\n", ttl/60)
	return nil
}

func refreshToken() error {
	if flagAPIKey == "" {
		return fmt.Errorf("token expired — run `tunnel login --api-key <key>` to re-authenticate")
	}
	token, _, err := fetchToken(flagAPIKey)
	if err != nil {
		return fmt.Errorf("token refresh failed: %w", err)
	}
	if err := writeToken(token); err != nil {
		return fmt.Errorf("save refreshed token: %w", err)
	}
	flagToken = token
	return nil
}

func fetchToken(apiKey string) (token string, ttlSeconds int64, err error) {
	httpBase := controlPlaneBase(flagServer)

	body, err := json.Marshal(map[string]string{"api_key": apiKey})
	if err != nil {
		return "", 0, fmt.Errorf("marshal request: %w", err)
	}

	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: flagInsecure,
			},
		},
	}

	resp, err := httpClient.Post(
		httpBase+"/auth/token",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return "", 0, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Token      string `json:"token"`
		TTLSeconds int64  `json:"ttl_seconds"`
		Error      string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", 0, fmt.Errorf("decode response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("login failed (%d): %s", resp.StatusCode, result.Error)
	}

	return result.Token, result.TTLSeconds, nil
}

func controlPlaneBase(tunnelAddr string) string {
	tunnelAddr = strings.TrimPrefix(tunnelAddr, "https://")
	tunnelAddr = strings.TrimPrefix(tunnelAddr, "http://")

	lastColon := strings.LastIndex(tunnelAddr, ":")
	if lastColon == -1 {
		return "http://" + tunnelAddr + ":8080"
	}

	host := tunnelAddr[:lastColon]
	port := tunnelAddr[lastColon+1:]

	if port == "4443" {
		return "http://" + host + ":8080"
	}

	return "https://" + host
}