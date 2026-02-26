package cmd

import (
	"fmt"
	"os"
	"runtime"

	"github.com/spf13/cobra"
	"github.com/the20100/g-docs-cli/internal/api"
	"github.com/the20100/g-docs-cli/internal/config"
)

var (
	jsonFlag   bool
	prettyFlag bool
	client     *api.Client
	cfg        *config.Config
)

var rootCmd = &cobra.Command{
	Use:   "gdocs",
	Short: "Google Docs CLI — manage Google Docs via the API",
	Long: `gdocs is a CLI tool for the Google Docs API.

It outputs JSON when piped (for agent use) and human-readable tables in a terminal.

Token resolution order:
  1. GDOCS_ACCESS_TOKEN env var
  2. Config file  (~/.config/gdocs/config.json  via: gdocs auth set-token)

To obtain an OAuth access token:
  - Using gcloud CLI:    gcloud auth print-access-token
  - Using OAuth 2.0:     gdocs auth login (requires GDOCS_CLIENT_ID + GDOCS_CLIENT_SECRET)

Examples:
  gdocs auth set-token <token>
  gdocs doc create "My Document"
  gdocs doc get <document-id>
  gdocs doc content <document-id>
  gdocs doc insert <document-id> "Hello, world!"
  gdocs doc replace <document-id> --find "old" --replace "new"`,
	SilenceUsage: true,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&jsonFlag, "json", false, "Force JSON output")
	rootCmd.PersistentFlags().BoolVar(&prettyFlag, "pretty", false, "Force pretty-printed JSON output (implies --json)")

	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if isAuthCommand(cmd) || cmd.Name() == "info" || cmd.Name() == "update" {
			return nil
		}
		token, err := resolveToken()
		if err != nil {
			return err
		}
		client = api.NewClient(token)
		return nil
	}

	rootCmd.AddCommand(infoCmd)
}

var infoCmd = &cobra.Command{
	Use:   "info",
	Short: "Show tool info: config path, auth status, and environment",
	Run: func(cmd *cobra.Command, args []string) {
		printInfo()
	},
}

func printInfo() {
	fmt.Printf("gdocs — Google Docs CLI\n\n")
	exe, _ := os.Executable()
	fmt.Printf("  binary:  %s\n", exe)
	fmt.Printf("  os/arch: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Println()
	fmt.Println("  config paths by OS:")
	fmt.Printf("    macOS:    ~/Library/Application Support/gdocs/config.json\n")
	fmt.Printf("    Linux:    ~/.config/gdocs/config.json\n")
	fmt.Printf("    Windows:  %%AppData%%\\gdocs\\config.json\n")
	fmt.Printf("  config:   %s\n", config.Path())
	fmt.Println()
	fmt.Printf("  GDOCS_ACCESS_TOKEN = %s\n", maskOrEmpty(os.Getenv("GDOCS_ACCESS_TOKEN")))
	fmt.Printf("  GDOCS_CLIENT_ID    = %s\n", maskOrEmpty(os.Getenv("GDOCS_CLIENT_ID")))
}

// resolveToken returns an OAuth access token from env var or stored config.
func resolveToken() (string, error) {
	if t := os.Getenv("GDOCS_ACCESS_TOKEN"); t != "" {
		return t, nil
	}
	var err error
	cfg, err = config.Load()
	if err != nil {
		return "", fmt.Errorf("failed to load config: %w", err)
	}
	if cfg.AccessToken != "" {
		return cfg.AccessToken, nil
	}
	return "", fmt.Errorf("not authenticated — run: gdocs auth set-token <token>\n" +
		"or set GDOCS_ACCESS_TOKEN env var\n\n" +
		"To get a token:\n" +
		"  gcloud auth print-access-token\n" +
		"  gdocs auth login  (requires GDOCS_CLIENT_ID + GDOCS_CLIENT_SECRET)")
}

func isAuthCommand(cmd *cobra.Command) bool {
	if cmd.Name() == "auth" {
		return true
	}
	p := cmd.Parent()
	for p != nil {
		if p.Name() == "auth" {
			return true
		}
		p = p.Parent()
	}
	return false
}
