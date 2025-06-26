package sharepoint

import (
	"converter_blob/types"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

func UploadFile(localPath, sharepointFolderPath string) (string, error) {
	cleanPath := strings.ReplaceAll(sharepointFolderPath, "\\", "/")
	cleanPath = strings.TrimPrefix(cleanPath, "/")
	cleanPath = strings.ReplaceAll(cleanPath, "//", "/")

	// Upload file dan kembalikan full path upload (untuk dicatat/share nanti)
	token := GetToken()
	siteID := os.Getenv("MS_SITE_ID")

	if token == "" || siteID == "" {
		return "", fmt.Errorf("❌ Token atau SiteID belum diset di environment variable")
	}

	client := resty.New()

	// Baca file lokal
	fileBytes, err := os.ReadFile(localPath)
	if err != nil {
		return "", fmt.Errorf("❌ Gagal membaca file lokal '%s': %w", localPath, err)
	}

	// Gabungkan kembali path folder + filename
	// Gunakan format path SharePoint (gunakan '/' bukan '\')
	fullSharePath := filepath.ToSlash(cleanPath)

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
		return "", fmt.Errorf("❌ Gagal mengupload ke SharePoint: %w", err)
	}
	if resp.IsError() {
		return "", fmt.Errorf("❌ Upload gagal (status %d): %s", resp.StatusCode(), resp.String())
	}
	fmt.Printf("📁 Berhasil diunggah ke SharePoint: %s\n", fullSharePath)

	return fullSharePath, nil
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
