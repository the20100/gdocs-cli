package cmd

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"time"

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

Authentication uses OAuth 2.0 or service accounts. Credentials are resolved in order:
  1. GDOCS_ACCESS_TOKEN env var (no refresh — short-lived)
  2. GOOGLE_APPLICATION_CREDENTIALS env var (service account JSON file)
  3. GDOCS_CREDENTIALS env var (service account JSON file)
  4. Config file (set with: gdocs auth login  OR  gdocs auth set-credentials)

Examples:
  gdocs auth login
  gdocs auth set-credentials /path/to/sa.json
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
		token, expiry, refreshFn, err := resolveCredentials()
		if err != nil {
			return err
		}
		client = api.NewClient(token, expiry, refreshFn)
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
	fmt.Printf("  GDOCS_ACCESS_TOKEN             = %s\n", maskOrEmpty(os.Getenv("GDOCS_ACCESS_TOKEN")))
	fmt.Printf("  GOOGLE_APPLICATION_CREDENTIALS = %s\n", maskOrEmpty(os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")))
	fmt.Printf("  GDOCS_CREDENTIALS              = %s\n", maskOrEmpty(os.Getenv("GDOCS_CREDENTIALS")))
	fmt.Printf("  GDOCS_CLIENT_ID                = %s\n", maskOrEmpty(os.Getenv("GDOCS_CLIENT_ID")))
}

// resolveEnv returns the value of the first non-empty environment variable.
func resolveEnv(names ...string) string {
	for _, name := range names {
		if v := os.Getenv(name); v != "" {
			return v
		}
	}
	return ""
}

// resolveCredentials returns a token, expiry, and optional refresh function.
func resolveCredentials() (string, int64, api.RefreshFunc, error) {
	// 1. Direct access token env var (no refresh capability)
	if token := resolveEnv(
		"GDOCS_ACCESS_TOKEN",
		"GDOCS_TOKEN",
		"GOOGLE_DOCS_ACCESS_TOKEN",
	); token != "" {
		return token, 0, nil, nil
	}

	// 2. Service account credentials file from env var
	if credFile := resolveEnv(
		"GOOGLE_APPLICATION_CREDENTIALS",
		"GDOCS_CREDENTIALS",
		"GOOGLE_CREDENTIALS",
		"GDOCS_SA_FILE",
	); credFile != "" {
		token, expiry, err := exchangeServiceAccountJWT(credFile, docsScope)
		if err != nil {
			return "", 0, nil, fmt.Errorf("service account auth failed: %w", err)
		}
		refreshFn := func() (string, int64, error) {
			return exchangeServiceAccountJWT(credFile, docsScope)
		}
		return token, expiry, refreshFn, nil
	}

	// 3. Config file
	var err error
	cfg, err = config.Load()
	if err != nil {
		return "", 0, nil, fmt.Errorf("failed to load config: %w", err)
	}

	// 3a. Service account file stored in config
	if cfg.CredentialsFile != "" {
		token, expiry, err := exchangeServiceAccountJWT(cfg.CredentialsFile, docsScope)
		if err != nil {
			return "", 0, nil, fmt.Errorf("service account auth failed: %w", err)
		}
		credFile := cfg.CredentialsFile
		refreshFn := func() (string, int64, error) {
			return exchangeServiceAccountJWT(credFile, docsScope)
		}
		return token, expiry, refreshFn, nil
	}

	// 3b. OAuth token stored in config
	if cfg.AccessToken != "" {
		var refreshFn api.RefreshFunc
		if cfg.RefreshToken != "" && cfg.ClientID != "" && cfg.ClientSecret != "" {
			refreshFn = func() (string, int64, error) {
				return doTokenRefresh(cfg.ClientID, cfg.ClientSecret, cfg.RefreshToken)
			}
		}
		return cfg.AccessToken, cfg.TokenExpiry, refreshFn, nil
	}

	return "", 0, nil, fmt.Errorf("not authenticated — run: gdocs auth login\nor set GDOCS_ACCESS_TOKEN env var\nor set GOOGLE_APPLICATION_CREDENTIALS to a service account file")
}

// doTokenRefresh exchanges a refresh token for a new access token.
func doTokenRefresh(clientID, clientSecret, refreshToken string) (string, int64, error) {
	params := url.Values{}
	params.Set("client_id", clientID)
	params.Set("client_secret", clientSecret)
	params.Set("refresh_token", refreshToken)
	params.Set("grant_type", "refresh_token")

	resp, err := http.PostForm("https://oauth2.googleapis.com/token", params)
	if err != nil {
		return "", 0, fmt.Errorf("token refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, fmt.Errorf("reading token response: %w", err)
	}

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", 0, fmt.Errorf("parsing token response: %w", err)
	}
	if result.Error != "" {
		return "", 0, fmt.Errorf("token refresh error: %s — %s", result.Error, result.ErrorDesc)
	}
	if result.AccessToken == "" {
		return "", 0, fmt.Errorf("no access_token in refresh response")
	}

	expiry := time.Now().Unix() + result.ExpiresIn

	// Persist the new token
	if cfg != nil {
		cfg.AccessToken = result.AccessToken
		cfg.TokenExpiry = expiry
		_ = config.Save(cfg)
	}

	return result.AccessToken, expiry, nil
}

// serviceAccountKey is the structure of a Google service account JSON key file.
type serviceAccountKey struct {
	Type        string `json:"type"`
	PrivateKey  string `json:"private_key"`
	ClientEmail string `json:"client_email"`
}

// exchangeServiceAccountJWT signs a JWT with the service account private key
// and exchanges it for a Google OAuth2 access token. Uses only stdlib crypto.
func exchangeServiceAccountJWT(credFile, scope string) (string, int64, error) {
	data, err := os.ReadFile(credFile)
	if err != nil {
		return "", 0, fmt.Errorf("reading credentials file: %w", err)
	}
	var sa serviceAccountKey
	if err := json.Unmarshal(data, &sa); err != nil {
		return "", 0, fmt.Errorf("parsing credentials file: %w", err)
	}
	if sa.Type != "service_account" {
		return "", 0, fmt.Errorf("unsupported credentials type %q (expected service_account)", sa.Type)
	}

	block, _ := pem.Decode([]byte(sa.PrivateKey))
	if block == nil {
		return "", 0, fmt.Errorf("no PEM block found in private key")
	}

	key, parseErr := x509.ParsePKCS8PrivateKey(block.Bytes)
	if parseErr != nil {
		// Try PKCS1 format as fallback
		rsaKey, err2 := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err2 != nil {
			return "", 0, fmt.Errorf("parsing private key: %w", parseErr)
		}
		key = rsaKey
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return "", 0, fmt.Errorf("private key is not RSA")
	}

	now := time.Now().Unix()
	exp := now + 3600

	headerJSON, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	claimsJSON, _ := json.Marshal(map[string]interface{}{
		"iss":   sa.ClientEmail,
		"scope": scope,
		"aud":   "https://oauth2.googleapis.com/token",
		"iat":   now,
		"exp":   exp,
	})

	headerEnc := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsEnc := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := headerEnc + "." + claimsEnc

	h := sha256.New()
	h.Write([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, rsaKey, crypto.SHA256, h.Sum(nil))
	if err != nil {
		return "", 0, fmt.Errorf("signing JWT: %w", err)
	}

	jwt := signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)

	params := url.Values{}
	params.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	params.Set("assertion", jwt)

	resp, err := http.PostForm("https://oauth2.googleapis.com/token", params)
	if err != nil {
		return "", 0, fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, fmt.Errorf("reading token response: %w", err)
	}

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", 0, fmt.Errorf("parsing token response: %w", err)
	}
	if result.Error != "" {
		return "", 0, fmt.Errorf("service account token error: %s — %s", result.Error, result.ErrorDesc)
	}
	if result.AccessToken == "" {
		return "", 0, fmt.Errorf("no access_token in response")
	}

	return result.AccessToken, time.Now().Unix() + result.ExpiresIn, nil
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
