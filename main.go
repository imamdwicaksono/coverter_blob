package main

import (
	"database/sql"
	"encoding/csv"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

const (
	exportFolder = "pdf_exports"
)

var (
	Version   = "dev"
	BuildTime = "unknown"
)

func printVersion() {
	fmt.Printf("📦 Versi: %s\n🕒 Dibangun: %s\n", Version, BuildTime)
}

func main() {
	env := flag.String("env", "", "Environment: dev, prod, atau kosong (default .env)")
	flag.StringVar(env, "e", "", "Alias untuk --env")
	singleFile := flag.String("file", "", "Path ke file PDF untuk diupload")
	folderPath := flag.String("folder", "", "Path ke folder PDF untuk diupload")
	extractFlag := flag.Bool("extract", false, "Ekstrak semua file dari DB ke folder")
	versionFlag := flag.Bool("version", false, "Tampilkan versi aplikasi")

	// Baru parse SEKALI saja
	flag.Parse()

	// Hitung flag utama
	modeFlags := 0
	if *singleFile != "" {
		modeFlags++
	}
	if *folderPath != "" {
		modeFlags++
	}
	if *extractFlag {
		modeFlags++
	}
	if *versionFlag {
		printVersion()
		return
	}

	// Validasi: hanya 1 dari 3 mode yang boleh aktif
	if modeFlags != 1 {
		fmt.Println("❌ Gunakan salah satu mode:")
		fmt.Println("   --file <file>    Upload satu file PDF")
		fmt.Println("   --folder <dir>   Upload semua PDF dari folder")
		fmt.Println("   --extract        Ekstrak semua PDF dari DB")
		fmt.Println("   (opsional) --env <env>  Pilih environment .env.dev / .env.prod")
		os.Exit(1)
	}

	if *env != "" {
		loadEnv(*env)
	}

	conn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		os.Getenv("DB_HOST"), os.Getenv("DB_PORT"), os.Getenv("DB_USER"),
		os.Getenv("DB_PASSWORD"), os.Getenv("DB_NAME"),
	)

	db, err := sql.Open("postgres", conn)
	if err != nil {
		log.Fatalf("❌ Gagal koneksi DB: %v", err)
	}
	defer db.Close()

	switch {
	case *singleFile != "":
		if err := uploadFile(db, *singleFile); err != nil {
			log.Printf("❌ Upload gagal: %v", err)
		}
	case *folderPath != "":
		uploadFolder(db, *folderPath)
	case *extractFlag:
		if err := extractAllFiles(db); err != nil {
			log.Fatalf("❌ Ekstrak gagal: %v", err)
		}
	default:
		fmt.Println("❗ Gunakan salah satu: --file <file>, --folder <dir>, atau --extract")
	}
}

func loadEnv(env string) {
	var envFile string

	switch env {
	case "dev":
		envFile = ".env.dev"
	case "prod":
		envFile = ".env.prod"
	default:
		envFile = ".env"
	}

	if _, err := os.Stat(envFile); err == nil {
		if err := godotenv.Load(envFile); err != nil {
			log.Fatalf("❌ Gagal load %s: %v", envFile, err)
		}
		fmt.Printf("📄 Environment loaded: %s\n", envFile)
	} else {
		fmt.Printf("⚠️  File %s tidak ditemukan, fallback ke system environment\n", envFile)
	}
}

func uploadFile(db *sql.DB, filePath string) error {
	data, err := ioutil.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("gagal baca file: %w", err)
	}
	fileName := filepath.Base(filePath)
	_, err = db.Exec("INSERT INTO pdf (file_name, file_data) VALUES ($1, $2)", fileName, data)
	if err != nil {
		return fmt.Errorf("gagal simpan ke DB: %w", err)
	}
	fmt.Printf("✅ Upload berhasil: %s\n", fileName)
	return nil
}

func uploadFolder(db *sql.DB, folder string) {
	files, err := os.ReadDir(folder)
	if err != nil {
		log.Fatalf("Gagal baca folder: %v", err)
	}
	for _, f := range files {
		if !f.IsDir() && strings.HasSuffix(strings.ToLower(f.Name()), ".pdf") {
			fullPath := filepath.Join(folder, f.Name())
			if err := uploadFile(db, fullPath); err != nil {
				log.Printf("❌ %s gagal: %v", f.Name(), err)
			}
		}
	}
}

func extractAllFiles(db *sql.DB) error {
	rows, err := db.Query(`select 
		doc_meta.filename AS file_name, 
		doc_meta.mime_type AS mime_type,
		doc_meta.file_type AS file_type, 
		fl.fullpath AS full_path, 
		doc_bl.pdf AS file_data
	from teradocu.document doc
	left join teradocu.document_metadata doc_meta on doc.id = doc_meta.document_id
	left join teradocu.folder fl on doc.folder_id = fl.id
	left join teradocu.document_binary_large doc_bl on doc.id = doc_bl.document_id
	where doc_bl.pdf IS NOT NULL
	;`)
	if err != nil {
		return fmt.Errorf("gagal query: %w", err)
	}
	defer rows.Close()

	// Siapkan CSV file
	metaFile, err := os.Create("extracted_metadata.csv")
	if err != nil {
		return fmt.Errorf("gagal buat CSV: %w", err)
	}
	defer metaFile.Close()
	writer := csv.NewWriter(metaFile)
	defer writer.Flush()
	writer.Write([]string{"file_name", "file_type", "mime_type", "full_path", "saved_path"})

	count := 0
	for rows.Next() {
		var fileName, mimeType, fileType, fullPath string
		var fileData []byte

		if err := rows.Scan(&fileName, &mimeType, &fileType, &fullPath, &fileData); err != nil {
			log.Printf("Gagal baca baris: %v", err)
			continue
		}

		// Tentukan ekstensi file
		ext := ".bin"
		if strings.TrimSpace(fileType) != "" {
			ext = "." + strings.TrimPrefix(strings.ToLower(fileType), ".")
		} else if strings.HasPrefix(mimeType, "application/pdf") {
			ext = ".pdf"
		} else if strings.Contains(mimeType, "word") {
			ext = ".docx"
		} else if strings.Contains(mimeType, "excel") {
			ext = ".xlsx"
		}

		// Normalisasi fullPath dan buat folder
		relDir := filepath.Clean(fullPath)
		saveDir := filepath.Join(exportFolder, relDir)
		if err := os.MkdirAll(saveDir, os.ModePerm); err != nil {
			log.Printf("❌ Gagal buat folder: %s | %v", saveDir, err)
			continue
		}

		outputName := fileName
		if !strings.HasSuffix(strings.ToLower(fileName), ext) {
			outputName += ext
		}
		outputPath := filepath.Join(saveDir, outputName)

		if err := os.WriteFile(outputPath, fileData, 0644); err != nil {
			log.Printf("❌ Gagal simpan file: %s | %v", outputPath, err)
			continue
		}
		fmt.Printf("📁 File disimpan: %s\n", outputPath)

		// Tulis metadata ke CSV
		writer.Write([]string{fileName, fileType, mimeType, fullPath, outputPath})
		count++
	}
	fmt.Printf("✅ Total file diekstrak: %d\n", count)
	return nil
}
