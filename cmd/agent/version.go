package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var version = "dev"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the tunnel version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("tunnel %s\n", version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}