package main

import (
	"database/sql"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"

	_ "github.com/lib/pq"
)

const outputFolder = "output" // Folder untuk menyimpan file hasil ekstrak

func extractBlob() {
	// DSN PostgreSQL
	psqlInfo := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		dbHost, dbPort, dbUser, dbPassword, dbName,
	)

	// Koneksi ke DB
	db, err := sql.Open("postgres", psqlInfo)
	if err != nil {
		log.Fatalf("Gagal koneksi ke DB: %v", err)
	}
	defer db.Close()

	// Pastikan folder output tersedia
	if err := os.MkdirAll(outputFolder, os.ModePerm); err != nil {
		log.Fatalf("Gagal membuat folder output: %v", err)
	}

	// Query data PDF
	rows, err := db.Query("SELECT file_name, file_data FROM pdf")
	if err != nil {
		log.Fatalf("Query gagal: %v", err)
	}
	defer rows.Close()

	// Simpan PDF
	for rows.Next() {
		var fileName string
		var fileData []byte

		if err := rows.Scan(&fileName, &fileData); err != nil {
			log.Printf("Gagal baca baris: %v", err)
			continue
		}

		filePath := filepath.Join(outputFolder, fileName)
		if err := ioutil.WriteFile(filePath, fileData, 0644); err != nil {
			log.Printf("Gagal simpan file %s: %v", fileName, err)
		} else {
			fmt.Printf("✅ File disimpan: %s\n", filePath)
		}
	}

	if err := rows.Err(); err != nil {
		log.Fatalf("Kesalahan saat membaca baris: %v", err)
	}
}
