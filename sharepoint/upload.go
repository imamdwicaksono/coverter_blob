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

	"bytes"
	"encoding/json"

	"github.com/go-resty/resty/v2"
	"github.com/schollz/progressbar/v3"
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

func UploadFileChunkedResume(localPath, sharepointFolderPath string) (string, error) {
	cleanPath := strings.ReplaceAll(sharepointFolderPath, "\\", "/")
	cleanPath = strings.TrimPrefix(cleanPath, "/")
	cleanPath = strings.ReplaceAll(cleanPath, "//", "/")

	token := GetToken()
	siteID := os.Getenv("MS_SITE_ID")

	if token == "" || siteID == "" {
		return "", fmt.Errorf("❌ Token atau SiteID belum diset di environment variable")
	}

	file, err := os.Open(localPath)
	if err != nil {
		return "", fmt.Errorf("❌ Gagal membuka file '%s': %w", localPath, err)
	}
	defer file.Close()

	fileInfo, _ := file.Stat()
	fileSize := fileInfo.Size()

	fullSharePath := filepath.ToSlash(cleanPath)
	escapedPath := escapeSharePointPath(fullSharePath)

	client := resty.New().
		SetTimeout(15 * time.Minute).
		AddRetryCondition(func(r *resty.Response, err error) bool {
			return r.StatusCode() == 503 || r.StatusCode() == 429 || err != nil
		}).
		SetRetryCount(3).
		SetRetryWaitTime(5 * time.Second).
		SetRetryMaxWaitTime(30 * time.Second)

	stateFile := localPath + ".uploadstate"
	var uploadURL string

	// 1️⃣ Cek status upload sebelumnya
	if savedState, err := os.ReadFile(stateFile); err == nil {
		var st UploadState
		if json.Unmarshal(savedState, &st) == nil && st.FilePath == localPath {
			fmt.Println("🔄 Melanjutkan upload dari session sebelumnya...")
			uploadURL = st.UploadURL
		}
	}

	// 2️⃣ Kalau tidak ada, buat session baru
	if uploadURL == "" {
		createSessionURL := fmt.Sprintf(
			"https://graph.microsoft.com/v1.0/sites/%s/drive/root:/%s:/createUploadSession",
			siteID, escapedPath,
		)
		resp, err := client.R().
			SetHeader("Authorization", "Bearer "+token).
			SetHeader("Content-Type", "application/json").
			Post(createSessionURL)
		if err != nil {
			return "", fmt.Errorf("❌ Gagal membuat upload session: %w", err)
		}
		if resp.IsError() {
			return "", fmt.Errorf("❌ Gagal membuat upload session (status %d): %s", resp.StatusCode(), resp.String())
		}

		var session UploadSessionResponse
		if err := json.Unmarshal(resp.Body(), &session); err != nil {
			return "", fmt.Errorf("❌ Gagal parse upload session: %w", err)
		}
		uploadURL = session.UploadURL

		saveState(stateFile, UploadState{UploadURL: uploadURL, FilePath: localPath})
	}

	// 3️⃣ Cek posisi terakhir dari server
	start := int64(0)
	rangeResp, err := client.R().
		SetHeader("Authorization", "Bearer "+token).
		Get(uploadURL)
	if err == nil && rangeResp.StatusCode() == 200 {
		var sessionInfo UploadSessionResponse
		if json.Unmarshal(rangeResp.Body(), &sessionInfo) == nil && len(sessionInfo.NextExpectedRanges) > 0 {
			fmt.Println("📌 Next expected range:", sessionInfo.NextExpectedRanges[0])
			fmt.Sscanf(sessionInfo.NextExpectedRanges[0], "%d-", &start)
		}
	}

	// 4️⃣ Progress bar setup
	bar := progressbar.NewOptions64(
		fileSize,
		progressbar.OptionSetDescription("📤 Uploading"),
		progressbar.OptionSetWriter(os.Stdout),
		progressbar.OptionShowBytes(true),
		progressbar.OptionSetWidth(15),
		progressbar.OptionThrottle(65*time.Millisecond),
		progressbar.OptionShowCount(),
		progressbar.OptionClearOnFinish(),
		progressbar.OptionSpinnerType(14),
		progressbar.OptionFullWidth(),
	)
	_ = bar.Add64(start) // kalau resume, mulai dari posisi terakhir

	// 5️⃣ Upload per chunk
	const chunkSize int64 = 5 * 1024 * 1024 // 5 MB
	for start < fileSize {
		end := start + chunkSize - 1
		if end >= fileSize {
			end = fileSize - 1
		}
		chunkLen := end - start + 1

		buf := make([]byte, chunkLen)
		_, err := file.ReadAt(buf, start)
		if err != nil && err != io.EOF {
			return "", fmt.Errorf("❌ Gagal membaca chunk: %w", err)
		}

		contentRange := fmt.Sprintf("bytes %d-%d/%d", start, end, fileSize)

		chunkResp, err := client.R().
			SetHeader("Authorization", "Bearer "+token).
			SetHeader("Content-Length", fmt.Sprintf("%d", chunkLen)).
			SetHeader("Content-Range", contentRange).
			SetBody(bytes.NewReader(buf)).
			Put(uploadURL)

		if err != nil {
			return "", fmt.Errorf("❌ Gagal upload chunk: %w", err)
		}

		if chunkResp.StatusCode() == 201 || chunkResp.StatusCode() == 200 {
			_ = bar.Finish()
			fmt.Println("\n✅ Upload selesai!")
			os.Remove(stateFile)
			return fullSharePath, nil
		}

		if chunkResp.StatusCode() != 308 {
			return "", fmt.Errorf("❌ Upload chunk gagal (status %d): %s", chunkResp.StatusCode(), chunkResp.String())
		}

		start = end + 1
		_ = bar.Add64(chunkLen)
	}

	return "", fmt.Errorf("❌ Upload tidak selesai, tapi tidak ada error fatal")
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
