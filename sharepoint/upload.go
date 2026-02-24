package sharepoint

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-resty/resty/v2"
)

var (
	httpClient *resty.Client
	once       sync.Once

	cachedToken string
	tokenExpiry time.Time
	tokenMu     sync.Mutex
)

// ================= INIT =================

func client() *resty.Client {

	once.Do(func() {

		httpClient = resty.New().
			SetTimeout(30 * time.Minute).
			AddRetryCondition(func(r *resty.Response, err error) bool {

				if err != nil {
					return true
				}

				if r.StatusCode() == 429 ||
					r.StatusCode() == 503 ||
					r.StatusCode() == 504 {
					return true
				}

				return false
			}).
			SetRetryCount(5).
			SetRetryWaitTime(3 * time.Second).
			SetRetryMaxWaitTime(30 * time.Second)
	})

	return httpClient
}

// ================= TOKEN CACHE =================

func GetToken() string {

	tokenMu.Lock()
	defer tokenMu.Unlock()

	if cachedToken != "" && time.Now().Before(tokenExpiry) {
		return cachedToken
	}

	c := client()

	tokenResp := struct {
		Token     string `json:"access_token"`
		ExpiresIn int    `json:"expires_in"`
	}{}

	form := map[string]string{
		"grant_type":    "client_credentials",
		"client_id":     os.Getenv("MS_CLIENT_ID"),
		"client_secret": os.Getenv("MS_CLIENT_SECRET"),
		"scope":         "https://graph.microsoft.com/.default",
	}

	resp, err := c.R().
		SetFormData(form).
		SetResult(&tokenResp).
		Post("https://login.microsoftonline.com/" +
			os.Getenv("MS_TENANT_ID") +
			"/oauth2/v2.0/token")

	if err != nil || resp.IsError() {
		return ""
	}

	cachedToken = tokenResp.Token

	// refresh sebelum expire
	tokenExpiry = time.Now().Add(
		time.Duration(tokenResp.ExpiresIn-300) * time.Second,
	)

	return cachedToken
}

// ================= SANITIZE =================

func sanitizeSPName(name string) string {

	invalid := []string{
		"#", "%", "&", "{", "}", "\\", "<", ">", "*",
		"?", "/", "$", "!", "'", "\"", ":", "@", "+",
	}

	for _, c := range invalid {
		name = strings.ReplaceAll(name, c, "_")
	}

	return name
}

func escapePath(p string) string {

	p = filepath.ToSlash(p)
	p = strings.TrimPrefix(p, "/")

	parts := strings.Split(p, "/")

	for i := range parts {
		parts[i] = url.PathEscape(
			sanitizeSPName(parts[i]),
		)
	}

	return strings.Join(parts, "/")
}

// ================= TYPES =================

type uploadSessionResp struct {
	UploadURL          string   `json:"uploadUrl"`
	ExpirationDateTime string   `json:"expirationDateTime"`
	NextExpectedRanges []string `json:"nextExpectedRanges,omitempty"`
}

type uploadState struct {
	UploadURL string `json:"uploadUrl"`
	FilePath  string `json:"filePath"`
}

// ================= MAIN UPLOAD =================

func UploadFileChunkedResumeV2(localPath, sharepointPath string) (string, error) {

	token := GetToken()
	driveID := os.Getenv("MS_DRIVE_ID")

	if token == "" || driveID == "" {
		return "", fmt.Errorf("❌ Token atau MS_DRIVE_ID belum diset")
	}

	// open file
	f, err := os.Open(localPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return "", err
	}

	fileSize := fi.Size()

	if fileSize == 0 {
		return "", fmt.Errorf("file kosong")
	}

	escapedPath := escapePath(sharepointPath)

	c := client()

	stateFile := localPath + ".uploadstate"
	var uploadURL string

	// ================= RESUME STATE =================

	if b, err := os.ReadFile(stateFile); err == nil {

		var st uploadState

		if json.Unmarshal(b, &st) == nil &&
			st.FilePath == localPath {

			uploadURL = st.UploadURL
		}
	}

	// ================= CREATE SESSION =================

	if uploadURL == "" {

		createURL := fmt.Sprintf(
			"https://graph.microsoft.com/v1.0/drives/%s/root:/%s:/createUploadSession",
			driveID,
			escapedPath,
		)

		body := map[string]interface{}{
			"item": map[string]interface{}{
				"@microsoft.graph.conflictBehavior": "replace",
				"name":                              filepath.Base(escapedPath),
			},
		}

		resp, err := c.R().
			SetHeader("Authorization", "Bearer "+token).
			SetHeader("Content-Type", "application/json").
			SetBody(body).
			Post(createURL)

		if err != nil {
			return "", err
		}

		if resp.IsError() {
			return "", fmt.Errorf("create session gagal: %s",
				resp.String())
		}

		var s uploadSessionResp

		if err := json.Unmarshal(resp.Body(), &s); err != nil {
			return "", err
		}

		if s.UploadURL == "" {
			return "", fmt.Errorf("uploadURL kosong")
		}

		uploadURL = s.UploadURL

		save := uploadState{
			UploadURL: uploadURL,
			FilePath:  localPath,
		}

		if b, err := json.Marshal(save); err == nil {
			_ = os.WriteFile(stateFile, b, 0644)
		}
	}

	// ================= HELPER NEXT RANGE =================

	getNextStart := func() int64 {

		r, err := c.R().
			SetHeader("Authorization", "Bearer "+token).
			Get(uploadURL)

		if err != nil || r.IsError() {
			return 0
		}

		var s uploadSessionResp

		if json.Unmarshal(r.Body(), &s) == nil &&
			len(s.NextExpectedRanges) > 0 {

			var start int64
			fmt.Sscanf(s.NextExpectedRanges[0], "%d-", &start)

			return start
		}

		return 0
	}

	start := getNextStart()

	// ================= CHUNK LOOP =================

	const chunkSize int64 = 10 * 1024 * 1024 // 10MB

	buf := make([]byte, chunkSize)

	for start < fileSize {

		remaining := fileSize - start

		readLen := chunkSize
		if remaining < chunkSize {
			readLen = remaining
		}

		n, err := f.ReadAt(buf[:readLen], start)
		if err != nil && err != io.EOF {
			return "", err
		}

		end := start + int64(n) - 1

		contentRange := fmt.Sprintf(
			"bytes %d-%d/%d",
			start,
			end,
			fileSize,
		)

		resp, err := c.R().
			SetHeader("Authorization", "Bearer "+token).
			SetHeader("Content-Length", fmt.Sprintf("%d", n)).
			SetHeader("Content-Range", contentRange).
			SetBody(bytes.NewReader(buf[:n])).
			Put(uploadURL)

		if err != nil {
			return "", err
		}

		// success
		if resp.StatusCode() == 200 ||
			resp.StatusCode() == 201 {

			_ = os.Remove(stateFile)

			return sharepointPath, nil
		}

		// partial
		if resp.StatusCode() == 202 ||
			resp.StatusCode() == 308 ||
			resp.StatusCode() == 204 {

			var s uploadSessionResp

			if json.Unmarshal(resp.Body(), &s) == nil &&
				len(s.NextExpectedRanges) > 0 {

				var next int64
				fmt.Sscanf(s.NextExpectedRanges[0], "%d-", &next)

				start = next
				continue
			}

			start = end + 1
			continue
		}

		// throttling
		if resp.StatusCode() == 429 {

			time.Sleep(10 * time.Second)
			continue
		}

		return "", fmt.Errorf(
			"upload gagal status %d: %s",
			resp.StatusCode(),
			resp.String(),
		)
	}

	_ = os.Remove(stateFile)

	return sharepointPath, nil
}
