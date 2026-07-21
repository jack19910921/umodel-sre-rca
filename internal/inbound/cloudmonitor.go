package inbound

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

var sensitiveKey = regexp.MustCompile(`(?i)(secret|password|authorization|access[_-]?key|signature|cookie|token)`) // values only; keys are retained

// Capture stores a redacted callback fixture. It is intentionally separate
// from alert parsing: the first real delivery defines the parser's field map.
type Capture struct{ dir string }

func NewCapture(dir string) *Capture { return &Capture{dir: dir} }

func (c *Capture) Save(ctx context.Context, raw []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if c.dir == "" {
		return "", fmt.Errorf("callback capture directory is not configured")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var payload any
	if err := decoder.Decode(&payload); err != nil {
		return "", fmt.Errorf("callback body must be valid JSON: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("callback body must contain exactly one JSON value")
	}
	if _, ok := payload.(map[string]any); !ok {
		return "", fmt.Errorf("callback body must be a JSON object")
	}
	redacted := redact(payload)
	encoded, err := json.MarshalIndent(redacted, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(c.dir, 0700); err != nil {
		return "", err
	}
	id, err := fixtureID()
	if err != nil {
		return "", err
	}
	path := filepath.Join(c.dir, id+".json")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	defer file.Close()
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		return "", err
	}
	return id, nil
}

func redact(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, child := range typed {
			if sensitiveKey.MatchString(key) {
				result[key] = "[REDACTED]"
				continue
			}
			result[key] = redact(child)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for i, child := range typed {
			result[i] = redact(child)
		}
		return result
	default:
		return value
	}
}

func fixtureID() (string, error) {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "cloudmonitor-" + time.Now().UTC().Format("20060102T150405.000000000Z") + "-" + hex.EncodeToString(buf), nil
}
