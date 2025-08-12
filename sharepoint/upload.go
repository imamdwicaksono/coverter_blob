package sharepoint

import (
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

// func SharingFolder() {
// 	// Siapkan body request untuk invite
// 	recipients := make([]map[string]string, 0, len(userEmails))
// 	for _, email := range userEmails {
// 		recipients = append(recipients, map[string]string{"email": email})
// 	}

// 	shareBody := map[string]interface{}{
// 		"recipients":     recipients,
// 		"message":        "File berhasil dibagikan melalui sistem.",
// 		"requireSignIn":  true,
// 		"sendInvitation": true,
// 		"roles":          []string{"read"},
// 	}

// 	shareFolderPath := filepath.Dir(fullSharePath)
// 	shareURL := fmt.Sprintf(
// 		"https://graph.microsoft.com/v1.0/sites/%s/drive/root:/%s:/invite",
// 		siteID, shareFolderPath,
// 	)

// 	shareResp, err := client.R().
// 		SetHeader("Authorization", "Bearer "+token).
// 		SetHeader("Content-Type", "application/json").
// 		SetBody(shareBody).
// 		Post(shareURL)

// 	if err != nil {
// 		return fmt.Errorf("❌ Gagal membagikan file: %w", err)
// 	}
// 	if shareResp.IsError() {
// 		return fmt.Errorf("❌ Gagal membagikan (status %d): %s", shareResp.StatusCode(), shareResp.String())
// 	}

// 	fmt.Printf("📨 Berhasil dibagikan ke: %s\n", strings.Join(userEmails, ", "))

// 	return nil
// }

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
