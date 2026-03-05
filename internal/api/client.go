package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const apiBase = "https://docs.googleapis.com"

// RefreshFunc is called when the access token is expired.
// It returns the new access token and its Unix expiry timestamp.
type RefreshFunc func() (newToken string, expiresAt int64, err error)

// Client is an authenticated Google Docs API client.
type Client struct {
	token       string
	tokenExpiry int64
	refreshFn   RefreshFunc
	httpClient  *http.Client
}

// NewClient creates a new Client using an OAuth Bearer token.
// refreshFn may be nil if no token refresh is needed.
func NewClient(token string, tokenExpiry int64, refreshFn RefreshFunc) *Client {
	return &Client{
		token:       token,
		tokenExpiry: tokenExpiry,
		refreshFn:   refreshFn,
		httpClient:  &http.Client{Timeout: 30 * time.Second},
	}
}

// ensureFreshToken refreshes the access token if it expires within 60 seconds.
func (c *Client) ensureFreshToken() error {
	if c.refreshFn == nil {
		return nil
	}
	if c.tokenExpiry > 0 && time.Now().Unix() < c.tokenExpiry-60 {
		return nil
	}
	newToken, newExpiry, err := c.refreshFn()
	if err != nil {
		return fmt.Errorf("token refresh failed: %w", err)
	}
	c.token = newToken
	c.tokenExpiry = newExpiry
	return nil
}

func (c *Client) doRequest(req *http.Request) ([]byte, error) {
	if err := c.ensureFreshToken(); err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var errResp struct {
			Error struct {
				Message string `json:"message"`
				Status  string `json:"status"`
			} `json:"error"`
		}
		if jerr := json.Unmarshal(body, &errResp); jerr == nil && errResp.Error.Message != "" {
			msg := errResp.Error.Message
			if errResp.Error.Status != "" {
				msg = errResp.Error.Status + ": " + msg
			}
			return nil, &DocsError{StatusCode: resp.StatusCode, Message: msg}
		}
		return nil, &DocsError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)),
		}
	}
	return body, nil
}

func (c *Client) get(path string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, apiBase+path, nil)
	if err != nil {
		return nil, err
	}
	return c.doRequest(req)
}

func (c *Client) post(path string, payload any) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encoding request: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, apiBase+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.doRequest(req)
}

// CreateDocument creates a new Google Doc with the given title.
func (c *Client) CreateDocument(title string) (*Document, error) {
	body, err := c.post("/v1/documents", map[string]string{"title": title})
	if err != nil {
		return nil, err
	}
	var doc Document
	return &doc, json.Unmarshal(body, &doc)
}

// GetDocument retrieves a document by its ID.
func (c *Client) GetDocument(documentID string) (*Document, error) {
	body, err := c.get("/v1/documents/" + documentID)
	if err != nil {
		return nil, err
	}
	var doc Document
	return &doc, json.Unmarshal(body, &doc)
}

// BatchUpdate applies one or more update requests to a document atomically.
func (c *Client) BatchUpdate(documentID string, req *BatchUpdateRequest) (*BatchUpdateResponse, error) {
	body, err := c.post("/v1/documents/"+documentID+":batchUpdate", req)
	if err != nil {
		return nil, err
	}
	var resp BatchUpdateResponse
	return &resp, json.Unmarshal(body, &resp)
}

// ExtractText traverses a Document's body and returns all text content.
func ExtractText(doc *Document) string {
	if doc.Body == nil {
		return ""
	}
	var sb strings.Builder
	for _, el := range doc.Body.Content {
		if el.Paragraph == nil {
			continue
		}
		for _, pe := range el.Paragraph.Elements {
			if pe.TextRun != nil {
				sb.WriteString(pe.TextRun.Content)
			}
		}
	}
	return sb.String()
}

// DocumentEndIndex returns the index just before the last newline in the document body.
// Used to insert text at the end of the document body.
func DocumentEndIndex(doc *Document) int {
	if doc.Body == nil || len(doc.Body.Content) == 0 {
		return 1
	}
	// The last structural element is always a section break at the very end.
	// We insert before it by using endOfSegmentLocation instead.
	last := doc.Body.Content[len(doc.Body.Content)-1]
	return last.EndIndex - 1
}
