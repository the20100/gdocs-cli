package cmd

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"encoding/json"

	"github.com/spf13/cobra"
	"github.com/the20100/g-docs-cli/internal/config"
)

const (
	googleAuthURL  = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL = "https://oauth2.googleapis.com/token"
	docsScope      = "https://www.googleapis.com/auth/documents"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage Google Docs authentication",
}

var authSetTokenCmd = &cobra.Command{
	Use:   "set-token <access-token>",
	Short: "Save a Google OAuth access token to the config file",
	Long: `Save a Google OAuth access token to the local config file.

How to get a token:
  1. Using gcloud CLI (easiest):
       gcloud auth print-access-token
     Note: gcloud tokens expire after 1 hour. Use 'gdocs auth login' for persistent auth.

  2. Using Google OAuth Playground:
       https://developers.google.com/oauthplayground/
     Select scope: https://www.googleapis.com/auth/documents

  3. Browser OAuth flow (persistent):
       gdocs auth login  (requires GDOCS_CLIENT_ID + GDOCS_CLIENT_SECRET)

The token is stored at:
  macOS:   ~/Library/Application Support/gdocs/config.json
  Linux:   ~/.config/gdocs/config.json
  Windows: %AppData%\gdocs\config.json

You can also set the GDOCS_ACCESS_TOKEN env var instead of using this command.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		token := args[0]
		if len(strings.TrimSpace(token)) < 10 {
			return fmt.Errorf("token looks too short — did you paste it correctly?")
		}
		if err := config.Save(&config.Config{AccessToken: token}); err != nil {
			return fmt.Errorf("saving config: %w", err)
		}
		fmt.Printf("Access token saved to %s\n", config.Path())
		fmt.Printf("Token: %s\n", maskOrEmpty(token))
		fmt.Println()
		fmt.Println("Note: gcloud tokens expire after ~1 hour.")
		fmt.Println("      For persistent auth, use: gdocs auth login")
		return nil
	},
}

var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in to Google via browser OAuth (persistent token)",
	Long: `Opens your browser to authenticate with Google and saves the access token.

Requires GDOCS_CLIENT_ID and GDOCS_CLIENT_SECRET environment variables.
These come from a Google Cloud project with the Google Docs API enabled.

Steps to set up OAuth credentials:
  1. Go to https://console.cloud.google.com/
  2. Create a project and enable the Google Docs API
  3. Go to APIs & Services > Credentials > Create Credentials > OAuth client ID
  4. Choose "Desktop app" as the application type
  5. Download the JSON and export the values:
       export GDOCS_CLIENT_ID=your_client_id
       export GDOCS_CLIENT_SECRET=your_client_secret
  6. Run: gdocs auth login`,
	RunE: runAuthLogin,
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current authentication status",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := config.Load()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		fmt.Printf("Config: %s\n\n", config.Path())
		if envToken := os.Getenv("GDOCS_ACCESS_TOKEN"); envToken != "" {
			fmt.Println("Token source: GDOCS_ACCESS_TOKEN env var (takes priority over config)")
			fmt.Printf("Token:        %s\n", maskOrEmpty(envToken))
		} else if c.AccessToken != "" {
			fmt.Println("Token source: config file")
			fmt.Printf("Token:        %s\n", maskOrEmpty(c.AccessToken))
		} else {
			fmt.Println("Status: not authenticated")
			fmt.Println()
			fmt.Println("Run one of:")
			fmt.Println("  gdocs auth set-token $(gcloud auth print-access-token)")
			fmt.Println("  gdocs auth login  (requires GDOCS_CLIENT_ID + GDOCS_CLIENT_SECRET)")
			fmt.Println("  export GDOCS_ACCESS_TOKEN=<token>")
		}
		return nil
	},
}

var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove the saved access token from the config file",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := config.Clear(); err != nil {
			return fmt.Errorf("removing config: %w", err)
		}
		fmt.Println("Access token removed from config.")
		return nil
	},
}

func init() {
	authCmd.AddCommand(authSetTokenCmd, authLoginCmd, authStatusCmd, authLogoutCmd)
	rootCmd.AddCommand(authCmd)
}

func runAuthLogin(cmd *cobra.Command, args []string) error {
	clientID := os.Getenv("GDOCS_CLIENT_ID")
	clientSecret := os.Getenv("GDOCS_CLIENT_SECRET")

	if clientID == "" {
		return fmt.Errorf("GDOCS_CLIENT_ID not set — see: gdocs auth login --help")
	}
	if clientSecret == "" {
		return fmt.Errorf("GDOCS_CLIENT_SECRET not set — see: gdocs auth login --help")
	}

	// Start a local HTTP server to receive the OAuth callback
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("finding free port: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if errMsg := q.Get("error"); errMsg != "" {
			errCh <- fmt.Errorf("OAuth error: %s — %s", errMsg, q.Get("error_description"))
			http.Error(w, "Authentication failed. You may close this tab.", http.StatusBadRequest)
			return
		}
		code := q.Get("code")
		if code == "" {
			errCh <- fmt.Errorf("no code returned in callback")
			http.Error(w, "No code received. You may close this tab.", http.StatusBadRequest)
			return
		}
		codeCh <- code
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html><html><body style="font-family:sans-serif;text-align:center;padding:40px">
<h2>Authentication successful!</h2>
<p>You may close this tab and return to the terminal.</p>
</body></html>`)
	})

	srv := &http.Server{Handler: mux}
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			select {
			case errCh <- fmt.Errorf("callback server error: %w", err):
			default:
			}
		}
	}()

	authURL := buildGoogleAuthURL(clientID, redirectURI)
	fmt.Println("\nOpening browser for Google authentication...")
	fmt.Printf("If the browser does not open automatically, visit:\n  %s\n\n", authURL)
	openBrowser(authURL)
	fmt.Printf("Waiting for callback on http://127.0.0.1:%d/callback ...\n", port)

	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		shutdownServer(srv)
		return err
	case <-time.After(5 * time.Minute):
		shutdownServer(srv)
		return fmt.Errorf("timed out waiting for OAuth callback (5 minutes)")
	}
	shutdownServer(srv)

	fmt.Println("Exchanging authorization code for token...")
	token, err := exchangeGoogleCode(code, clientID, clientSecret, redirectURI)
	if err != nil {
		return fmt.Errorf("token exchange failed: %w", err)
	}

	if err := config.Save(&config.Config{AccessToken: token}); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	fmt.Printf("\nAccess token saved to %s\n", config.Path())
	fmt.Printf("Token: %s\n", maskOrEmpty(token))
	return nil
}

func buildGoogleAuthURL(clientID, redirectURI string) string {
	params := url.Values{}
	params.Set("client_id", clientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("scope", docsScope)
	params.Set("response_type", "code")
	params.Set("access_type", "offline")
	params.Set("prompt", "consent")
	return googleAuthURL + "?" + params.Encode()
}

func exchangeGoogleCode(code, clientID, clientSecret, redirectURI string) (string, error) {
	params := url.Values{}
	params.Set("code", code)
	params.Set("client_id", clientID)
	params.Set("client_secret", clientSecret)
	params.Set("redirect_uri", redirectURI)
	params.Set("grant_type", "authorization_code")

	resp, err := http.PostForm(googleTokenURL, params)
	if err != nil {
		return "", fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading token response: %w", err)
	}

	var result struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parsing token response: %w", err)
	}
	if result.Error != "" {
		return "", fmt.Errorf("token error: %s — %s", result.Error, result.ErrorDesc)
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("no access_token in response: %s", string(body))
	}
	return result.AccessToken, nil
}

func openBrowser(u string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	_ = cmd.Start()
}

func shutdownServer(srv *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
