package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type FileSignature struct {
	Magic     []byte
	Extension string
	Mime      string
}

var knownSignatures = []FileSignature{
	{[]byte{0x25, 0x50, 0x44, 0x46}, ".pdf", "application/pdf"},
	{[]byte{0x50, 0x4B, 0x03, 0x04}, ".docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
	{[]byte{0xD0, 0xCF, 0x11, 0xE0}, ".doc", "application/msword"},
	{[]byte{0x50, 0x4B, 0x03, 0x04}, ".xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
	{[]byte{0x50, 0x4B, 0x03, 0x04}, ".pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
	{[]byte{0x89, 0x50, 0x4E, 0x47}, ".png", "image/png"},
	{[]byte{0xFF, 0xD8, 0xFF}, ".jpg", "image/jpeg"},
}

func detectFileType(data []byte) (string, string) {
	for _, sig := range knownSignatures {
		if len(data) < len(sig.Magic) {
			continue
		}
		if string(data[:len(sig.Magic)]) == string(sig.Magic) {
			return sig.Extension, sig.Mime
		}
	}
	return "unknown", "unknown"
}

func main() {
	exportFolder := "pdf_exports"
	total, mismatch, unknown := 0, 0, 0

	filepath.Walk(exportFolder, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Printf("❌ Tidak bisa dibaca: %s\n", path)
			return nil
		}

		if len(data) < 4 {
			fmt.Printf("⚠️  File terlalu kecil: %s\n", path)
			return nil
		}

		total++
		actualExt, actualMime := detectFileType(data)
		currentExt := strings.ToLower(filepath.Ext(path))

		if actualExt == "unknown" {
			fmt.Printf("❓ Tidak dikenali: %s (magic: %x)\n", path, data[:4])
			unknown++
			return nil
		}

		if actualExt != currentExt {
			fmt.Printf("❌ Salah ekstensi: %s → seharusnya %s (%s)\n", path, actualExt, actualMime)
			mismatch++
		} else {
			fmt.Printf("✅ Valid: %s (%s)\n", path, actualMime)
		}
		return nil
	})

	fmt.Printf("\n📊 Total: %d | Salah Ekstensi: %d | Tidak Dikenali: %d\n", total, mismatch, unknown)
}
