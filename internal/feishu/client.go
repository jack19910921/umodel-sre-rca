package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/jack/umodel-sre-rca/internal/domain"
)

const maxCardBytes = 30 * 1024

type Client struct {
	baseURL   string
	appID     string
	appSecret string
	chatID    string
	http      *http.Client
}

func NewClient(baseURL, appID, appSecret, chatID string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), appID: appID, appSecret: appSecret, chatID: chatID, http: httpClient}
}

func (c *Client) CreateIncidentCard(ctx context.Context, incident domain.Incident) (string, error) {
	content, err := RenderIncidentCard(incident, domain.RCAResult{})
	if err != nil {
		return "", err
	}
	token, err := c.tenantAccessToken(ctx)
	if err != nil {
		return "", err
	}
	var response struct {
		Code int `json:"code"`
		Data struct {
			MessageID string `json:"message_id"`
		} `json:"data"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/im/v1/messages?receive_id_type=chat_id", token, map[string]any{"receive_id": c.chatID, "msg_type": "interactive", "content": string(content)}, &response); err != nil {
		return "", err
	}
	if response.Code != 0 || response.Data.MessageID == "" {
		return "", fmt.Errorf("Feishu create message failed: code=%d", response.Code)
	}
	return response.Data.MessageID, nil
}

func (c *Client) UpdateIncidentCard(ctx context.Context, messageID string, incident domain.Incident, result domain.RCAResult) error {
	if messageID == "" {
		return fmt.Errorf("Feishu message id is required")
	}
	content, err := RenderIncidentCard(incident, result)
	if err != nil {
		return err
	}
	token, err := c.tenantAccessToken(ctx)
	if err != nil {
		return err
	}
	var response struct {
		Code int `json:"code"`
	}
	if err := c.doJSON(ctx, http.MethodPatch, "/im/v1/messages/"+messageID, token, map[string]any{"msg_type": "interactive", "content": string(content)}, &response); err != nil {
		return err
	}
	if response.Code != 0 {
		return fmt.Errorf("Feishu update message failed: code=%d", response.Code)
	}
	return nil
}

func (c *Client) tenantAccessToken(ctx context.Context) (string, error) {
	var response struct {
		Code  int    `json:"code"`
		Token string `json:"tenant_access_token"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/auth/v3/tenant_access_token/internal", "", map[string]string{"app_id": c.appID, "app_secret": c.appSecret}, &response); err != nil {
		return "", err
	}
	if response.Code != 0 || response.Token == "" {
		return "", fmt.Errorf("Feishu tenant token request failed: code=%d", response.Code)
	}
	return response.Token, nil
}

func (c *Client) doJSON(ctx context.Context, method, path, token string, body, output any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Feishu %s %s returned HTTP %d", method, path, response.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(output); err != nil {
		return fmt.Errorf("decode Feishu response: %w", err)
	}
	return nil
}

func RenderIncidentCard(incident domain.Incident, result domain.RCAResult) ([]byte, error) {
	elements := []map[string]any{
		{"tag": "div", "fields": []map[string]string{{"is_short": "true", "text": "**Incident**\n" + incident.ID}, {"is_short": "true", "text": "**状态**\n" + incident.State}}},
	}
	if result.Summary != "" {
		elements = append(elements, map[string]any{"tag": "div", "text": map[string]string{"tag": "lark_md", "content": "**结论**\n" + result.Summary}})
		elements = append(elements, map[string]any{"tag": "div", "text": map[string]string{"tag": "lark_md", "content": fmt.Sprintf("**置信度**\n%.0f%%", result.Confidence*100)}})
	}
	if len(result.EvidenceIDs) > 0 {
		elements = append(elements, map[string]any{"tag": "div", "text": map[string]string{"tag": "lark_md", "content": "**证据引用**\n" + strings.Join(result.EvidenceIDs, ", ")}})
	}
	if len(result.NextActions) > 0 {
		elements = append(elements, map[string]any{"tag": "div", "text": map[string]string{"tag": "lark_md", "content": "**建议动作**\n- " + strings.Join(result.NextActions, "\n- ")}})
	}
	card := map[string]any{
		"config":   map[string]bool{"wide_screen_mode": true},
		"header":   map[string]any{"title": map[string]string{"tag": "plain_text", "content": "SRE RCA · " + incident.State}, "template": cardTemplate(incident.State)},
		"elements": elements,
	}
	raw, err := json.Marshal(card)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxCardBytes {
		return nil, fmt.Errorf("Feishu card is %d bytes; maximum is %d", len(raw), maxCardBytes)
	}
	return raw, nil
}

func cardTemplate(state string) string {
	switch state {
	case domain.IncidentCompleted:
		return "green"
	case domain.IncidentFailed:
		return "red"
	case domain.IncidentRecovered:
		return "blue"
	default:
		return "orange"
	}
}
