package sharepoint

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
)

type Permission struct {
	ID        string   `json:"id"`
	Roles     []string `json:"roles"`
	GrantedTo struct {
		User struct {
			DisplayName string `json:"displayName"`
			Email       string `json:"email"`
		} `json:"user"`
	} `json:"grantedTo"`
}

func GetAccessListPermission(folderPath string) error {

	accessToken := GetToken()
	siteID := os.Getenv("MS_SITE_ID")
	if accessToken == "" || siteID == "" {
		return fmt.Errorf("❌ Token atau SiteID belum diset di environment variable")
	}

	item, err := GetItemIDFromPath(accessToken, siteID, folderPath)
	if err != nil {
		return err
	}
	itemID := item.ID

	fmt.Printf("📂 Mengambil daftar akses untuk item: %s (ID: %s)\n", item.Name, itemID)

	url := fmt.Sprintf("https://graph.microsoft.com/v1.0/sites/%s/drive/items/%s/permissions", siteID, itemID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := ioutil.ReadAll(resp.Body)
		fmt.Printf("❌ Gagal request: %s\n%s\n", resp.Status, body)
		return fmt.Errorf("Gagal request: %s\n%s\n", resp.Status, body)
	}

	var result struct {
		Value []Permission `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	fmt.Println("📋 Daftar Akses:")
	for _, p := range result.Value {
		fmt.Printf("- %s (%s): %v\n", p.GrantedTo.User.DisplayName, p.GrantedTo.User.Email, p.Roles)
	}
	return nil
}
