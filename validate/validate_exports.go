package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	exportFolder := "pdf_exports"
	count := 0
	badFiles := 0

	err := filepath.Walk(exportFolder, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			fmt.Printf("❌ Error akses file %s: %v\n", path, err)
			return nil
		}
		if info.IsDir() {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Printf("❌ Gagal baca file %s: %v\n", path, err)
			badFiles++
			return nil
		}

		if len(data) == 0 {
			fmt.Printf("⚠️ Kosong: %s\n", path)
			badFiles++
			return nil
		}

		magic := string(data[:4])
		ext := filepath.Ext(path)

		valid := false
		switch ext {
		case ".pdf":
			if magic == "%PDF" {
				valid = true
			}
		case ".docx", ".xlsx", ".pptx", ".zip":
			if magic[:2] == "PK" {
				valid = true
			}
		case ".doc":
			if magic == "\xd0\xcf\x11\xe0" {
				valid = true // old Word format (OLE2)
			}
		case ".txt":
			valid = true // cannot validate by magic
		}

		if valid {
			fmt.Printf("✅ OK: %s (%d bytes)\n", path, len(data))
		} else {
			fmt.Printf("❌ Tidak valid: %s (magic: %x)\n", path, data[:4])
			badFiles++
		}
		count++
		return nil
	})

	if err != nil {
		fmt.Printf("🚫 Gagal validasi: %v\n", err)
		return
	}

	fmt.Printf("\n🔍 Total diperiksa: %d | Rusak/tidak valid: %d\n", count, badFiles)
}
