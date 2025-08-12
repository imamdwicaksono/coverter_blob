package sharepoint

import (
	"bytes"
	"converter_blob/types"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"encoding/json"

	"github.com/go-resty/resty/v2"
)

type FolderAccessMap map[string]map[string]bool // Folder → Email set

// UploadToSharePointAndShare mengunggah file ke SharePoint dan membagikannya ke banyak pengguna.
func UploadToSharePointAndShare(localPath, sharepointFolderPath, timestamp string, fileName string, userEmails []string) error {
	token := GetToken()
	siteID := os.Getenv("MS_SITE_ID")

	if token == "" || siteID == "" {
		return fmt.Errorf("❌ Token atau SiteID belum diset di environment variable")
	}

	client := resty.New()

	// Baca file lokal
	fileBytes, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("❌ Gagal membaca file lokal '%s': %w", localPath, err)
	}

	parts := strings.Split(filepath.ToSlash(sharepointFolderPath), "/")
	parts[0] = timestamp + "_" + parts[0]
	versionedFolder := filepath.Join(parts...)
	fullSharePath := filepath.ToSlash(filepath.Join(versionedFolder, fileName))

	uploadURL := fmt.Sprintf(
		"https://graph.microsoft.com/v1.0/sites/%s/drive/root:/%s:/content",
		siteID, fullSharePath,
	)

	// Upload file ke SharePoint
	resp, err := client.R().
		SetHeader("Authorization", "Bearer "+token).
		SetHeader("Content-Type", "application/octet-stream").
		SetBody(fileBytes).
		Put(uploadURL)

	if err != nil {
		return fmt.Errorf("❌ Gagal mengupload ke SharePoint: %w", err)
	}
	if resp.IsError() {
		return fmt.Errorf("❌ Upload gagal (status %d): %s", resp.StatusCode(), resp.String())
	}
	fmt.Printf("📁 Berhasil diunggah ke SharePoint: %s\n", fullSharePath)

	return nil
}

type UploadSessionResponse struct {
	UploadURL          string   `json:"uploadUrl"`
	ExpirationDateTime string   `json:"expirationDateTime"`
	NextExpectedRanges []string `json:"nextExpectedRanges,omitempty"`
}

type UploadState struct {
	UploadURL string `json:"uploadUrl"`
	FilePath  string `json:"filePath"`
}

func UploadFileChunkedResume(localPath, sharepointPath string) (string, error) {
	file, err := os.Open(localPath)
	if err != nil {
		return "", fmt.Errorf("❌ Gagal membuka file: %w", err)
	}
	defer file.Close()

	fi, _ := file.Stat()
	fileSize := fi.Size()
	chunkSize := int64(5 * 1024 * 1024) // 5MB
	totalChunks := (fileSize + chunkSize - 1) / chunkSize

	token := GetToken()
	siteID := os.Getenv("MS_SITE_ID")
	if token == "" || siteID == "" {
		return "", fmt.Errorf("❌ Token/SiteID belum diset")
	}

	// Create upload session
	cleanPath := url.PathEscape(strings.TrimPrefix(filepath.ToSlash(sharepointPath), "/"))
	sessionURL := fmt.Sprintf("https://graph.microsoft.com/v1.0/sites/%s/drive/root:/%s:/createUploadSession", siteID, cleanPath)
	resp, err := resty.New().
		R().
		SetHeader("Authorization", "Bearer "+token).
		SetBody(map[string]interface{}{
			"item": map[string]interface{}{
				"@microsoft.graph.conflictBehavior": "replace",
				"name":                              filepath.Base(sharepointPath),
			},
		}).
		Post(sessionURL)
	if err != nil {
		return "", fmt.Errorf("❌ Gagal membuat upload session: %w", err)
	}

	var sessionData struct {
		UploadURL string `json:"uploadUrl"`
	}
	if err := json.Unmarshal(resp.Body(), &sessionData); err != nil {
		return "", fmt.Errorf("❌ Gagal parsing upload URL: %w", err)
	}

	uploadURL := sessionData.UploadURL
	buffer := make([]byte, chunkSize)
	var start int64 = 0
	client := resty.New().SetTimeout(5 * time.Minute)

	for chunkIndex := int64(0); chunkIndex < totalChunks; chunkIndex++ {
		bytesRead, readErr := file.Read(buffer)
		if readErr != nil && readErr != io.EOF {
			return "", fmt.Errorf("❌ Gagal baca chunk: %w", readErr)
		}

		end := start + int64(bytesRead) - 1
		contentRange := fmt.Sprintf("bytes %d-%d/%d", start, end, fileSize)

		r, err := client.R().
			SetHeader("Authorization", "Bearer "+token).
			SetHeader("Content-Length", fmt.Sprintf("%d", bytesRead)).
			SetHeader("Content-Range", contentRange).
			SetBody(buffer[:bytesRead]).
			Put(uploadURL)

		if err != nil {
			return "", fmt.Errorf("❌ Gagal upload chunk %d: %w", chunkIndex, err)
		}

		// 202 = masih lanjut, 201/200 = selesai
		if r.StatusCode() == 201 || r.StatusCode() == 200 {
			fmt.Printf("✔️ Upload selesai: %s\n", sharepointPath)
			return sharepointPath, nil
		}

		start += int64(bytesRead)
	}

	return sharepointPath, nil
}

// Struct untuk parsing createUploadSession response / session info
type uploadSessionResp struct {
	UploadURL          string   `json:"uploadUrl"`
	ExpirationDateTime string   `json:"expirationDateTime"`
	NextExpectedRanges []string `json:"nextExpectedRanges,omitempty"`
}

type uploadState struct {
	UploadURL string `json:"uploadUrl"`
	FilePath  string `json:"filePath"`
}

// UploadFileChunkedResumeV2 = chunked upload + resume + robust handling 202/308 + debug logs
func UploadFileChunkedResumeV2(localPath, sharepointFolderPath string) (string, error) {
	// prepare
	token := GetToken()
	siteID := os.Getenv("MS_SITE_ID")
	if token == "" || siteID == "" {
		return "", fmt.Errorf("❌ Token atau MS_SITE_ID belum diset")
	}

	// open file
	f, err := os.Open(localPath)
	if err != nil {
		return "", fmt.Errorf("❌ Gagal membuka file '%s': %w", localPath, err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("❌ Gagal stat file '%s': %w", localPath, err)
	}
	fileSize := fi.Size()
	if fileSize == 0 {
		return "", fmt.Errorf("❌ File kosong: %s", localPath)
	}

	// sanitize sharepoint path (escape components, keep /)
	cleanPath := strings.ReplaceAll(sharepointFolderPath, "\\", "/")
	cleanPath = strings.TrimPrefix(cleanPath, "/")
	cleanPath = strings.ReplaceAll(cleanPath, "//", "/")
	parts := strings.Split(cleanPath, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	escapedPath := strings.Join(parts, "/")

	// resty client
	client := resty.New().
		SetTimeout(15 * time.Minute).
		AddRetryCondition(func(r *resty.Response, err error) bool {
			return err != nil || r.StatusCode() == 429 || r.StatusCode() == 503
		}).
		SetRetryCount(3).
		SetRetryWaitTime(2 * time.Second).
		SetRetryMaxWaitTime(10 * time.Second)

	// state file for resume
	stateFile := localPath + ".uploadstate"
	var uploadURL string

	// try read saved state
	if b, err := os.ReadFile(stateFile); err == nil {
		var st uploadState
		if json.Unmarshal(b, &st) == nil && st.FilePath == localPath && st.UploadURL != "" {
			uploadURL = st.UploadURL
		}
	}

	// if no saved session, create one (with small retry/backoff)
	if uploadURL == "" {
		createURL := fmt.Sprintf("https://graph.microsoft.com/v1.0/sites/%s/drive/root:/%s:/createUploadSession", siteID, escapedPath)

		// body: instruct replace on conflict so we don't get 409
		body := map[string]interface{}{
			"item": map[string]interface{}{
				"@microsoft.graph.conflictBehavior": "replace",
				"name":                              filepath.Base(cleanPath),
			},
		}

		var createResp *resty.Response
		var createErr error
		attempts := 3
		for i := 0; i < attempts; i++ {
			createResp, createErr = client.R().
				SetHeader("Authorization", "Bearer "+token).
				SetHeader("Content-Type", "application/json").
				SetBody(body).
				Post(createURL)

			if createErr == nil && !createResp.IsError() {
				break
			}
			// small backoff
			time.Sleep(time.Duration((i+1)*2) * time.Second)
		}

		if createErr != nil {
			return "", fmt.Errorf("❌ Gagal createUploadSession: %w", createErr)
		}
		if createResp.IsError() {
			return "", fmt.Errorf("❌ createUploadSession gagal (status %d): %s", createResp.StatusCode(), createResp.String())
		}

		var s uploadSessionResp
		if err := json.Unmarshal(createResp.Body(), &s); err != nil {
			return "", fmt.Errorf("❌ Gagal parse createUploadSession response: %w -- body: %s", err, createResp.String())
		}
		if s.UploadURL == "" {
			return "", fmt.Errorf("❌ UploadURL kosong dari createUploadSession -- body: %s", createResp.String())
		}
		uploadURL = s.UploadURL

		// save state
		save := uploadState{UploadURL: uploadURL, FilePath: localPath}
		if b, err := json.Marshal(save); err == nil {
			_ = os.WriteFile(stateFile, b, 0644)
		}
	}

	// Helper: get next expected start from session info (GET uploadURL)
	getNextStart := func() (int64, error) {
		r, err := client.R().
			SetHeader("Authorization", "Bearer "+token).
			Get(uploadURL)
		if err != nil {
			return 0, fmt.Errorf("❌ Gagal GET session info: %w", err)
		}
		// sometimes server returns 200 + body with nextExpectedRanges
		if r.IsError() {
			// if 404/410 the session expired
			return 0, fmt.Errorf("❌ GET session info gagal (status %d): %s", r.StatusCode(), r.String())
		}
		var s uploadSessionResp
		if err := json.Unmarshal(r.Body(), &s); err == nil {
			if len(s.NextExpectedRanges) > 0 {
				// nextExpectedRanges format: "0-" or "12345-"
				var start int64
				fmt.Sscanf(s.NextExpectedRanges[0], "%d-", &start)
				return start, nil
			}
		}
		// fallback: if header "Range" exists (308 responses sometimes include it)
		if rng := r.Header().Get("Range"); rng != "" {
			// Range: bytes=0-499
			var a, b int64
			if n, _ := fmt.Sscanf(rng, "bytes=%d-%d", &a, &b); n == 2 {
				return b + 1, nil
			}
		}
		// nothing known, assume 0
		return 0, nil
	}

	// determine start (resume)
	var start int64 = 0
	if uploadURL != "" {
		if s, err := getNextStart(); err == nil && s > 0 {
			start = s
		}
	}

	// chunk upload loop
	const chunkSize int64 = 5 * 1024 * 1024 // 5MB
	buf := make([]byte, chunkSize)

	for start < fileSize {
		remaining := fileSize - start
		readLen := chunkSize
		if remaining < chunkSize {
			readLen = remaining
		}

		// read exact part
		n, err := f.ReadAt(buf[:readLen], start)
		if err != nil && err != io.EOF {
			return "", fmt.Errorf("❌ Gagal baca file chunk at %d: %w", start, err)
		}
		if int64(n) != readLen {
			// short read — adjust
			readLen = int64(n)
		}

		end := start + readLen - 1
		contentRange := fmt.Sprintf("bytes %d-%d/%d", start, end, fileSize)

		// do PUT
		resp, err := client.R().
			SetHeader("Authorization", "Bearer "+token).
			SetHeader("Content-Length", fmt.Sprintf("%d", readLen)).
			SetHeader("Content-Range", contentRange).
			SetBody(bytes.NewReader(buf[:readLen])).
			Put(uploadURL)

		if err != nil {
			return "", fmt.Errorf("❌ Gagal upload chunk (start=%d end=%d): %w", start, end, err)
		}

		// success final
		if resp.StatusCode() == 201 || resp.StatusCode() == 200 {
			// remove state file
			_ = os.Remove(stateFile)
			// optionally return item metadata from resp.Body()
			return filepath.ToSlash(strings.TrimPrefix(sharepointFolderPath, "/")), nil
		}

		// partial accepted — server expects more
		if resp.StatusCode() == 202 || resp.StatusCode() == 308 || resp.StatusCode() == 204 {
			// try to parse nextExpectedRanges from response body
			var s uploadSessionResp
			if err := json.Unmarshal(resp.Body(), &s); err == nil && len(s.NextExpectedRanges) > 0 {
				var nextStart int64
				fmt.Sscanf(s.NextExpectedRanges[0], "%d-", &nextStart)
				if nextStart > start {
					start = nextStart
					continue
				}
			}

			// else do GET session info to ask server what's next
			if next, err := getNextStart(); err == nil && next > start {
				start = next
				continue
			}

			// fallback: advance by readLen if server doesn't provide nextExpectedRanges
			start = end + 1
			continue
		}

		// any other response -> error out and include body for debugging
		return "", fmt.Errorf("❌ Upload chunk gagal (status %d): %s", resp.StatusCode(), resp.String())
	}

	// loop finished but no final confirmation — do a final GET to check status
	if sStart, err := getNextStart(); err == nil {
		if sStart >= fileSize {
			_ = os.Remove(stateFile)
			return filepath.ToSlash(strings.TrimPrefix(sharepointFolderPath, "/")), nil
		}
	}

	return "", fmt.Errorf("❌ Upload tidak selesai dan server tidak mengembalikan final response")
}

func saveState(filename string, state UploadState) {
	data, _ := json.Marshal(state)
	os.WriteFile(filename, data, 0644)
}

func escapeSharePointPath(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

func ShareFolderOnly(folderPath string, emailAccess []types.EmailAccess) error {
	cleanPath := strings.ReplaceAll(folderPath, "\\", "/")
	cleanPath = strings.TrimPrefix(cleanPath, "/")
	cleanPath = strings.ReplaceAll(cleanPath, "//", "/")

	token := GetToken()
	siteID := os.Getenv("MS_SITE_ID")
	if token == "" || siteID == "" {
		return fmt.Errorf("❌ Token atau SiteID belum diset di environment variable")
	}

	// Gunakan format path SharePoint (gunakan '/' bukan '\')
	spFolderPath := strings.TrimPrefix(filepath.ToSlash(cleanPath), "/")

	for _, access := range emailAccess {

		// if access.Email != "firman.sahlani@mmsgroup.co.id" || access.Email != "rio.ariandi@mmsgroup.co.id" {
		// 	continue // Lewati jika email tidak sesuai
		// }

		shareBody := map[string]interface{}{
			"recipients":     []string{"imam.dwicaksono@mmsgroup.co.id", "eldin.akbar@mmsgroup.co.id"},
			"message":        "Akses folder diberikan melalui sistem.",
			"requireSignIn":  true,
			"sendInvitation": true,
			"roles":          []string{access.SharepointRole.RolePermission},
		}
		fmt.Print(resty.New().JSONMarshal(shareBody))

		client := resty.New()
		shareURL := fmt.Sprintf(
			"https://graph.microsoft.com/v1.0/sites/%s/drive/root:/%s:/invite",
			siteID, spFolderPath,
		)

		resp, err := client.R().
			SetHeader("Authorization", "Bearer "+token).
			SetHeader("Content-Type", "application/json").
			SetBody(shareBody).
			Post(shareURL)

		if err != nil {
			return fmt.Errorf("❌ Gagal melakukan permintaan share: %w", err)
		}
		if resp.IsError() {
			return fmt.Errorf("❌ Gagal share folder (status %d): %s", resp.StatusCode(), resp.String())
		}

		fmt.Printf("📨 Folder '%s' berhasil dibagikan ke: %s\n", spFolderPath, access.Email)
	}

	return nil
}

func GetToken() string {
	client := resty.New()
	// 1. Dapatkan token
	tokenResp := struct {
		Token string `json:"access_token"`
	}{}

	tokenReq := map[string]string{
		"grant_type":    "client_credentials",
		"client_id":     os.Getenv("MS_CLIENT_ID"),
		"client_secret": os.Getenv("MS_CLIENT_SECRET"),
		"scope":         "https://graph.microsoft.com/.default",
	}

	resp, err := client.R().
		SetFormData(tokenReq).
		SetResult(&tokenResp).
		Post("https://login.microsoftonline.com/" + os.Getenv("MS_TENANT_ID") + "/oauth2/v2.0/token")

	if err != nil {
		return ""
	}
	token := tokenResp.Token

	if resp.IsError() {
		return ""
	}
	return token
}
