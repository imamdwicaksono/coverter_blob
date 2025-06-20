package sharepoint

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
)

type ItemResponse struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	WebUrl string `json:"webUrl"`
}

func GetItemIDFromPath(accessToken, siteID, path string) (*ItemResponse, error) {
	// Encode spasi jadi %20, karena path perlu URL encoded
	encodedPath := strings.ReplaceAll(path, " ", "%20")
	url := fmt.Sprintf("https://graph.microsoft.com/v1.0/sites/%s/drive/root:/%s", siteID, encodedPath)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("❌ gagal membuat request: %w", err)
	}

	req.Header.Add("Authorization", "Bearer "+accessToken)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("❌ gagal melakukan request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := ioutil.ReadAll(resp.Body)
		return nil, fmt.Errorf("❌ response error (%d): %s", resp.StatusCode, string(body))
	}

	var item ItemResponse
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		return nil, fmt.Errorf("❌ gagal decode response: %w", err)
	}

	return &item, nil
}
