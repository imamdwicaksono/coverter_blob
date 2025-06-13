package main

import (
	"database/sql"
	"fmt"
	"io/ioutil"
	"log"
	"path/filepath"

	_ "github.com/lib/pq"
)

func upload_file_to_db() {
	// Koneksi ke PostgreSQL
	psqlInfo := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		dbHost, dbPort, dbUser, dbPassword, dbName,
	)

	db, err := sql.Open("postgres", psqlInfo)
	if err != nil {
		log.Fatalf("Gagal koneksi DB: %v", err)
	}
	defer db.Close()

	// Baca file ke memory
	fileBytes, err := ioutil.ReadFile(filePath)
	if err != nil {
		log.Fatalf("Gagal membaca file: %v", err)
	}

	// Ambil nama file
	fileName := filepath.Base(filePath)

	// Simpan ke DB
	_, err = db.Exec("INSERT INTO pdf (file_name, file_data) VALUES ($1, $2)", fileName, fileBytes)
	if err != nil {
		log.Fatalf("Gagal insert ke DB: %v", err)
	}

	fmt.Printf("✅ Berhasil upload file '%s' ke database.\n", fileName)
}
