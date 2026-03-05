package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/the20100/g-docs-cli/internal/config"
)

const (
	googleAuthURL  = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL = "https://oauth2.googleapis.com/token"
	googleUserInfo = "https://www.googleapis.com/oauth2/v2/userinfo"
	docsScope      = "https://www.googleapis.com/auth/documents"
	// oauthLoginScope adds userinfo scopes so we can display who is logged in.
	oauthLoginScope = docsScope + " https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage Google Docs authentication",
}

var authLoginNoBrowser bool
var authLoginClientSecretFile string

var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in to Google Docs via browser OAuth 2.0",
	Long: `Opens your browser to authenticate with Google and saves the credentials.

Client credentials (client_id + client_secret) are resolved in order:
  1. GDOCS_CLIENT_ID + GDOCS_CLIENT_SECRET env vars
  2. Config (set with: gdocs auth set-client-secret)
  3. GDOCS_CLIENT_SECRET_FILE env var (path to client_secret.json)
  4. --client-secret-file flag
  5. Default path: $XDG_CONFIG_HOME/google/client_secret.json
     (Linux: ~/.config/google/client_secret.json, macOS: ~/Library/Application Support/google/client_secret.json)

Create OAuth 2.0 credentials at: https://console.cloud.google.com/apis/credentials
Choose "Desktop application" as the application type.

On a remote server (VPS) where no browser is available:
  gdocs auth login --no-browser`,
	RunE: runAuthLogin,
}

var authSetTokenCmd = &cobra.Command{
	Use:   "set-token <access-token>",
	Short: "Save an access token directly (no browser needed, no auto-refresh)",
	Long: `Saves a Google Docs access token directly to the config file.

Note: tokens saved this way cannot be auto-refreshed. To get a long-lived
setup with automatic token refresh, use: gdocs auth login

You can also set GDOCS_ACCESS_TOKEN as an env var for one-off use.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		token := args[0]
		if len(token) < 8 {
			return fmt.Errorf("token looks too short")
		}
		email, name, err := fetchUserInfo(token)
		if err != nil {
			return fmt.Errorf("token validation failed: %w", err)
		}
		newCfg := &config.Config{
			AccessToken: token,
			UserEmail:   email,
			UserName:    name,
		}
		if err := config.Save(newCfg); err != nil {
			return fmt.Errorf("saving config: %w", err)
		}
		fmt.Printf("Token saved — authenticated as %s (%s)\n", name, email)
		fmt.Printf("Config: %s\n", config.Path())
		return nil
	},
}

var authSetClientSecretCmd = &cobra.Command{
	Use:   "set-client-secret <path-to-client_secret.json>",
	Short: "Save the path to a client_secret.json file for OAuth 2.0 login",
	Long: `Save the path to a Google OAuth 2.0 client_secret.json file.

Download client_secret.json from:
  https://console.cloud.google.com/apis/credentials
Choose "Desktop application" as the application type.

Once set, you can log in without any env vars:
  gdocs auth login

Default lookup path (used automatically if the file exists):
  Linux:   ~/.config/google/client_secret.json
  macOS:   ~/Library/Application Support/google/client_secret.json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := args[0]
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("file not found: %s", path)
		}
		// Verify the file is parseable
		if _, _, err := loadClientSecretFile(path); err != nil {
			return fmt.Errorf("invalid client_secret.json: %w", err)
		}
		c, err := config.Load()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		c.ClientSecretFile = path
		if err := config.Save(c); err != nil {
			return fmt.Errorf("saving config: %w", err)
		}
		fmt.Printf("client_secret.json path saved to %s\n", config.Path())
		fmt.Printf("File: %s\n", path)
		fmt.Printf("\nRun: gdocs auth login\n")
		return nil
	},
}

var authSetCredentialsCmd = &cobra.Command{
	Use:   "set-credentials <path-to-service-account-json>",
	Short: "Save a service account credentials file path to the config",
	Long: `Save the path to a Google service account JSON key file.

To create a service account:
  1. Go to https://console.cloud.google.com/iam-admin/serviceaccounts
  2. Create a service account and share your documents with its email
  3. Create a JSON key and download it
  4. Run: gdocs auth set-credentials /path/to/key.json

Alternatively, set the GOOGLE_APPLICATION_CREDENTIALS env var.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := args[0]
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("credentials file not found: %s", path)
		}
		if err := config.Save(&config.Config{CredentialsFile: path}); err != nil {
			return fmt.Errorf("saving config: %w", err)
		}
		fmt.Printf("Credentials path saved to %s\n", config.Path())
		fmt.Printf("File: %s\n", path)
		return nil
	},
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

		if v := os.Getenv("GDOCS_ACCESS_TOKEN"); v != "" {
			fmt.Println("Token source: GDOCS_ACCESS_TOKEN env var (takes priority over config)")
			fmt.Printf("Token:        %s\n", maskOrEmpty(v))
		} else if v := resolveEnv("GOOGLE_APPLICATION_CREDENTIALS", "GDOCS_CREDENTIALS"); v != "" {
			fmt.Println("Source: service account credentials file (env var)")
			fmt.Printf("File:   %s\n", v)
		} else if c.CredentialsFile != "" {
			fmt.Println("Source: service account credentials file (config)")
			fmt.Printf("File:   %s\n", c.CredentialsFile)
		} else if c.AccessToken != "" {
			if c.UserName != "" {
				fmt.Printf("Authenticated as: %s (%s)\n", c.UserName, c.UserEmail)
			}
			fmt.Printf("Token source:     config file (OAuth)\n")
			fmt.Printf("Token:            %s\n", maskOrEmpty(c.AccessToken))
			if c.RefreshToken != "" {
				fmt.Println("Auto-refresh:     enabled")
			} else {
				fmt.Println("Auto-refresh:     disabled (no refresh token)")
			}
			if c.TokenExpiry > 0 {
				expiry := time.Unix(c.TokenExpiry, 0)
				if time.Now().Before(expiry) {
					fmt.Printf("Token expires:    %s\n", expiry.UTC().Format("2006-01-02 15:04 UTC"))
				} else {
					fmt.Printf("Token expires:    expired at %s\n", expiry.UTC().Format("2006-01-02 15:04 UTC"))
				}
			}
		} else {
			fmt.Println("Status: not authenticated")
			fmt.Printf("\nRun: gdocs auth login\nOr:  export GDOCS_ACCESS_TOKEN=<token>\nOr:  gdocs auth set-credentials /path/to/sa.json\n")
		}

		// Show OAuth client credential source
		fmt.Println()
		clientID, _, src, _ := resolveClientCredentials("")
		if clientID != "" {
			fmt.Printf("OAuth client:     %s (%s)\n", maskOrEmpty(clientID), src)
		} else {
			fmt.Printf("OAuth client:     (not configured)\n")
			fmt.Printf("  Set via: export GDOCS_CLIENT_ID=...\n")
			fmt.Printf("  Or:      gdocs auth set-client-secret /path/to/client_secret.json\n")
			if def := defaultClientSecretPath(); def != "" {
				fmt.Printf("  Default: %s\n", def)
			}
		}
		return nil
	},
}

var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove saved credentials from the config file",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := config.Clear(); err != nil {
			return fmt.Errorf("removing config: %w", err)
		}
		fmt.Println("Credentials removed from config.")
		return nil
	},
}

func init() {
	authLoginCmd.Flags().BoolVar(&authLoginNoBrowser, "no-browser", false, "Manual auth flow for remote/VPS: print the URL, prompt for the redirect URL")
	authLoginCmd.Flags().StringVar(&authLoginClientSecretFile, "client-secret-file", "", "Path to client_secret.json (overrides default lookup)")
	authCmd.AddCommand(authLoginCmd, authSetTokenCmd, authSetClientSecretCmd, authSetCredentialsCmd, authStatusCmd, authLogoutCmd)
	rootCmd.AddCommand(authCmd)
}

func runAuthLogin(cmd *cobra.Command, args []string) error {
	clientID, clientSecret, src, err := resolveClientCredentials(authLoginClientSecretFile)
	if err != nil {
		return fmt.Errorf("no OAuth client credentials found\n\n%w\n\nCreate credentials at: https://console.cloud.google.com/apis/credentials\nThen set: export GDOCS_CLIENT_ID=... GDOCS_CLIENT_SECRET=...\nOr:       gdocs auth set-client-secret /path/to/client_secret.json", err)
	}
	fmt.Printf("Using client credentials from: %s\n", src)

	var code string
	var redirectURI string

	if authLoginNoBrowser {
		redirectURI = "http://localhost:8080"
		authURL := buildAuthURL(clientID, redirectURI)
		var err error
		code, err = runOAuthFlowManual(authURL)
		if err != nil {
			return err
		}
	} else {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return fmt.Errorf("finding free port: %w", err)
		}
		port := listener.Addr().(*net.TCPAddr).Port
		redirectURI = fmt.Sprintf("http://127.0.0.1:%d/callback", port)

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

		authURL := buildAuthURL(clientID, redirectURI)
		fmt.Printf("\nOpening browser for Google authentication...\n")
		fmt.Printf("If the browser does not open, visit:\n  %s\n\n", authURL)
		openBrowser(authURL)
		fmt.Printf("Waiting for callback on http://127.0.0.1:%d/callback ...\n", port)

		select {
		case code = <-codeCh:
		case err = <-errCh:
			shutdownServer(srv)
			return err
		case <-time.After(5 * time.Minute):
			shutdownServer(srv)
			return fmt.Errorf("timed out waiting for OAuth callback (5 minutes)")
		}
		shutdownServer(srv)
	}

	fmt.Println("Exchanging authorization code for tokens...")
	accessToken, refreshToken, expiry, err := exchangeCode(code, clientID, clientSecret, redirectURI)
	if err != nil {
		return fmt.Errorf("exchanging code: %w", err)
	}

	fmt.Println("Fetching user info...")
	email, name, err := fetchUserInfo(accessToken)
	if err != nil {
		return fmt.Errorf("fetching user info: %w", err)
	}

	newCfg := &config.Config{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenExpiry:  expiry,
		ClientID:     clientID,
		ClientSecret: clientSecret,
		UserEmail:    email,
		UserName:     name,
	}
	if err := config.Save(newCfg); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	fmt.Printf("\nAuthenticated as %s (%s)\n", name, email)
	fmt.Printf("Auto-refresh: enabled\n")
	fmt.Printf("Config saved to: %s\n", config.Path())
	return nil
}

// clientSecretJSON is the structure of a Google OAuth 2.0 client_secret.json file.
type clientSecretJSON struct {
	Installed *clientSecretApp `json:"installed"`
	Web       *clientSecretApp `json:"web"`
}

type clientSecretApp struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// loadClientSecretFile parses a client_secret.json and returns clientID, clientSecret.
func loadClientSecretFile(path string) (string, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("reading %s: %w", path, err)
	}
	var f clientSecretJSON
	if err := json.Unmarshal(data, &f); err != nil {
		return "", "", fmt.Errorf("parsing client_secret.json: %w", err)
	}
	app := f.Installed
	if app == nil {
		app = f.Web
	}
	if app == nil {
		return "", "", fmt.Errorf("no 'installed' or 'web' section in %s", path)
	}
	if app.ClientID == "" || app.ClientSecret == "" {
		return "", "", fmt.Errorf("missing client_id or client_secret in %s", path)
	}
	return app.ClientID, app.ClientSecret, nil
}

// defaultClientSecretPath returns the OS-specific default path for client_secret.json.
func defaultClientSecretPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "google", "client_secret.json")
}

// resolveClientCredentials returns clientID, clientSecret, source description, and error.
// flagFile is the value of --client-secret-file flag (may be empty).
// Resolution order:
//  1. GDOCS_CLIENT_ID + GDOCS_CLIENT_SECRET env vars
//  2. Config stored ClientID + ClientSecret
//  3. GDOCS_CLIENT_SECRET_FILE env var → parse file
//  4. flagFile (--client-secret-file flag) → parse file
//  5. Config ClientSecretFile → parse file
//  6. Default path $UserConfigDir/google/client_secret.json (if exists)
func resolveClientCredentials(flagFile string) (clientID, clientSecret, source string, err error) {
	// 1. Direct env vars
	clientID = os.Getenv("GDOCS_CLIENT_ID")
	clientSecret = os.Getenv("GDOCS_CLIENT_SECRET")
	if clientID != "" && clientSecret != "" {
		return clientID, clientSecret, "env vars (GDOCS_CLIENT_ID / GDOCS_CLIENT_SECRET)", nil
	}

	// 2. Config stored values
	if c, err2 := config.Load(); err2 == nil && c != nil {
		if clientID == "" {
			clientID = c.ClientID
		}
		if clientSecret == "" {
			clientSecret = c.ClientSecret
		}
		if clientID != "" && clientSecret != "" {
			return clientID, clientSecret, "config file (stored credentials)", nil
		}

		// 3. GDOCS_CLIENT_SECRET_FILE env var
		if path := os.Getenv("GDOCS_CLIENT_SECRET_FILE"); path != "" {
			if id, sec, err2 := loadClientSecretFile(path); err2 == nil {
				return id, sec, "GDOCS_CLIENT_SECRET_FILE → " + path, nil
			}
		}

		// 4. --client-secret-file flag
		if flagFile != "" {
			if id, sec, err2 := loadClientSecretFile(flagFile); err2 == nil {
				return id, sec, "--client-secret-file → " + flagFile, nil
			}
		}

		// 5. Config ClientSecretFile
		if c.ClientSecretFile != "" {
			if id, sec, err2 := loadClientSecretFile(c.ClientSecretFile); err2 == nil {
				return id, sec, "config client_secret_file → " + c.ClientSecretFile, nil
			}
		}
	}

	// 6. Default path
	if def := defaultClientSecretPath(); def != "" {
		if _, statErr := os.Stat(def); statErr == nil {
			if id, sec, err2 := loadClientSecretFile(def); err2 == nil {
				return id, sec, "default path → " + def, nil
			}
		}
	}

	return "", "", "", fmt.Errorf("no client credentials found")
}

func runOAuthFlowManual(authURL string) (string, error) {
	fmt.Printf("\nOpen the following URL in your browser:\n\n%s\n\n", authURL)
	fmt.Println("After authorizing, your browser will be redirected to localhost:8080.")
	fmt.Println("That page will fail to load — that's expected on a remote server.")
	fmt.Println("Copy the full URL from the browser's address bar and paste it below.")
	fmt.Print("\nRedirect URL: ")

	reader := bufio.NewReader(os.Stdin)
	rawURL, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("reading redirect URL: %w", err)
	}
	rawURL = strings.TrimSpace(rawURL)

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parsing redirect URL: %w", err)
	}

	if errMsg := parsed.Query().Get("error"); errMsg != "" {
		return "", fmt.Errorf("authorization failed: %s", errMsg)
	}

	code := parsed.Query().Get("code")
	if code == "" {
		return "", fmt.Errorf("no authorization code found in URL — make sure you copied the full redirect URL")
	}

	return code, nil
}

func buildAuthURL(clientID, redirectURI string) string {
	params := url.Values{}
	params.Set("client_id", clientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("scope", oauthLoginScope)
	params.Set("response_type", "code")
	params.Set("access_type", "offline")
	params.Set("prompt", "consent")
	return googleAuthURL + "?" + params.Encode()
}

func exchangeCode(code, clientID, clientSecret, redirectURI string) (string, string, int64, error) {
	params := url.Values{}
	params.Set("code", code)
	params.Set("client_id", clientID)
	params.Set("client_secret", clientSecret)
	params.Set("redirect_uri", redirectURI)
	params.Set("grant_type", "authorization_code")

	resp, err := http.PostForm(googleTokenURL, params)
	if err != nil {
		return "", "", 0, fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", 0, fmt.Errorf("reading token response: %w", err)
	}

	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Error        string `json:"error"`
		ErrorDesc    string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", "", 0, fmt.Errorf("parsing token response: %w", err)
	}
	if result.Error != "" {
		return "", "", 0, fmt.Errorf("OAuth error: %s — %s", result.Error, result.ErrorDesc)
	}
	if result.AccessToken == "" {
		return "", "", 0, fmt.Errorf("no access_token in response: %s", string(body))
	}

	expiry := time.Now().Unix() + result.ExpiresIn
	return result.AccessToken, result.RefreshToken, expiry, nil
}

func fetchUserInfo(token string) (email, name string, err error) {
	req, err := http.NewRequest(http.MethodGet, googleUserInfo, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("userinfo request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("reading userinfo response: %w", err)
	}

	var result struct {
		Email string `json:"email"`
		Name  string `json:"name"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", "", fmt.Errorf("parsing userinfo response: %w", err)
	}
	if result.Error != nil {
		return "", "", fmt.Errorf("userinfo error: %s", result.Error.Message)
	}
	return result.Email, result.Name, nil
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
