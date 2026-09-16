package uploader

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"

	"github.com/yourusername/rl-replay-uploader/internal/httpclient"
)

const uploadURL = "https://ballchasing.com/api/v2/upload"
const pingURL = "https://ballchasing.com/api/"

// ValidateToken pings ballchasing.com with the given token to
// confirm it's actually accepted before an account gets saved with
// it. This is what backs the "confirm" step in the add-account flow.
func ValidateToken(token string) error {
	req, err := http.NewRequest(http.MethodGet, pingURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", token)

	resp, err := httpclient.Client().Do(req)
	if err != nil {
		return fmt.Errorf("contacting ballchasing: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("ballchasing rejected this token (401 unauthorized)")
	}
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected response from ballchasing (%d): %s", resp.StatusCode, data)
	}
	return nil
}

type UploadResult struct {
	ID       string `json:"id"`
	Location string `json:"location"`
}

// UploadReplay POSTs a .replay file to ballchasing.gg.
// visibility is one of: "public", "unlisted", "private".
func UploadReplay(token, filePath, visibility string) (*UploadResult, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("opening replay file: %w", err)
	}
	defer f.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(part, f); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s?visibility=%s", uploadURL, visibility)
	req, err := http.NewRequest(http.MethodPost, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := httpclient.Client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// 201 Created = new upload, 409 Conflict = duplicate (already uploaded)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusConflict {
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ballchasing upload failed (%d): %s", resp.StatusCode, data)
	}

	var result UploadResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}
