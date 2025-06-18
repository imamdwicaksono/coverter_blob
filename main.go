package main

import (
	"converter_blob/logs"
	"converter_blob/sharepoint"
	"converter_blob/types"
	"database/sql"
	"encoding/csv"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	withUploadSharepointFlag := flag.Bool("with-upload-sp", false, "tidak Sertakan upload ke SharePoint")

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
		if err := extractAllFiles(db, *withUploadSharepointFlag); err != nil {
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

func extractAllFiles(db *sql.DB, withUploadSharepoint bool) error {
	rows, err := db.Query(` 
		SELECT DISTINCT ON (doc_bl.document_id)
    doc_meta.filename AS file_name,
    doc_meta.mime_type AS mime_type,
    doc_meta.file_type AS file_type,
    fl.fullpath AS full_path,
    doc_bl.pdf AS file_data,
    fl.id AS folder_id,
    lo_get(doc_bl.binary) AS file_data_binary
FROM teradocu.document_binary_large doc_bl
JOIN teradocu.document doc ON doc.id = doc_bl.document_id
LEFT JOIN teradocu.document_metadata doc_meta ON doc.id = doc_meta.document_id
LEFT JOIN teradocu.folder fl ON doc.folder_id = fl.id
ORDER BY doc_bl.document_id, doc_bl.version DESC
	`)
	if err != nil {
		return fmt.Errorf("gagal query: %w", err)
	}
	defer rows.Close()

	metaFile, err := os.Create("extracted_metadata.csv")
	if err != nil {
		return fmt.Errorf("gagal buat CSV: %w", err)
	}
	defer metaFile.Close()
	writer := csv.NewWriter(metaFile)
	defer writer.Flush()
	writer.Write([]string{"file_name", "file_type", "mime_type", "full_path", "saved_path"})

	count := 0
	// Timestamp format (tanpa karakter ":" karena ilegal di SharePoint paths)
	timestamp := time.Now().Format("2006-01-02T15-04-05")
	accessMap := make(map[string]map[string]bool) // folder → user set
	for rows.Next() {
		var (
			fileName       string
			mimeType       string
			fileType       string
			fullPath       string
			fileData       []byte
			folderId       string
			fileDataBinary []byte
		)

		err := rows.Scan(&fileName, &mimeType, &fileType, &fullPath, &fileData, &folderId, &fileDataBinary)
		if err != nil {
			fmt.Printf("❌ Gagal scan baris: %v\n", err)
			continue
		}
		if fileData == nil {
			fileData = fileDataBinary
		}

		// // Ganti dengan folder ID yang sesuai
		// listUserFolder, err := GetUserByFolderId(folderId, db)
		// if err != nil {
		// 	log.Fatalf("❌ Gagal mendapatkan akses folder: %v", err)
		// }
		// if len(listUserFolder) == 0 {
		// 	log.Printf("⚠️  Tidak ada user yang memiliki akses ke folder ID %d", folderId)
		// }

		// // Logging ke file
		// logWriterUserAccess := logs.SetLog("user_access.txt")
		// defer logs.LogFlush(logWriterUserAccess)

		// for _, userAccess := range listUserFolder {
		// 	email := userAccess.Email
		// 	fID := userAccess.FolderId

		// 	logLine := fmt.Sprintf("[%s] User %s memiliki akses ke folder ID %d\n", time.Now().Format(time.RFC3339), email, fID)
		// 	logWriterUserAccess.WriteString(logLine)
		// 	log.Print("📂", logLine)

		// 	// Simpan ke dalam map
		// 	// accessMap[fID] = append(accessMap[fID], email)
		// }

		// Buat direktori sesuai struktur full_path
		safeFolder := filepath.Join(exportFolder, filepath.FromSlash(fullPath))
		if err := os.MkdirAll(safeFolder, os.ModePerm); err != nil {
			// log.Printf("❌ Gagal buat folder %s: %v", safeFolder, err)
			continue
		}

		outputName := sanitizeFileName(fileName)
		outputPath := filepath.Join(safeFolder, outputName)

		if _, err := os.Stat(outputPath); err == nil {
			// log.Printf("♻️  File sudah ada, menimpa: %s", outputPath)
		}

		err = os.WriteFile(outputPath, fileData, 0644)
		if err != nil {
			// log.Printf("❌ Gagal simpan file %s: %v", outputPath, err)
			continue
		}

		actualExt, _ := detectFileType(fileData)
		currentExt := filepath.Ext(outputPath)

		if actualExt != currentExt && actualExt != "unknown" {
			newPath := strings.TrimSuffix(outputPath, currentExt) + actualExt
			if err := os.Rename(outputPath, newPath); err != nil {
				// log.Printf("❌ Gagal rename ke %s: %v", newPath, err)
			} else {
				// log.Printf("🔁 Ekstensi diperbaiki: %s → %s", outputPath, newPath)
				outputPath = newPath
			}
		} else {
			// log.Printf("✅ Ekstensi valid: %s (%s)", filepath.Base(outputPath), actualMime)
		}

		writer.Write([]string{
			fileName,
			fileType,
			mimeType,
			fullPath,
			outputPath,
		})

		// Share ke user tertentu (bisa dinamis berdasarkan metadata)
		// spPath := filepath.ToSlash(fullPath) // pastikan path menggunakan `/` untuk SharePoint
		// err = sharepoint.UploadToSharePointAndShare(
		// 	outputPath,
		// 	spPath,
		// 	timestamp,
		// 	sanitizeFileName(fileName),
		// 	[]string{"imam.dwicaksono@mmsgroup.co.id"}, // "gayatri.vijayakumari@mmsgroup.co.id"

		// )
		// if err != nil {
		// 	fmt.Println("❌ ERROR:", err)
		// }

		// Dalam loop per dokumen:

		// spPath := filepath.ToSlash(fullPath) // pastikan path menggunakan `/` untuk SharePoint

		// Remove "pdf_exports" prefix from outputPath for cleanFullPath
		cleanFolderPath := strings.TrimPrefix(outputPath, exportFolder+string(os.PathSeparator))
		cleanFolderPath = strings.Trim(cleanFolderPath, "/\\")
		folderPath := fmt.Sprintf("%s/%s", timestamp, cleanFolderPath)

		// if withUploadSharepoint {
		sharepoint.UploadFile(outputPath, folderPath)
		// }

		folderKey := fmt.Sprintf("%s/%s", timestamp, fullPath)

		if _, ok := accessMap[folderKey]; !ok {
			accessMap[folderKey] = make(map[string]bool)
		}
		accessMap[folderKey]["imam.dwicaksono@mmsgroup.co.id"] = true
		// accessMap[folderKey]["rio.ariandi@mmsgroup.co.id"] = true
		count++
	}

	logWriter := logs.SetLog("sharepoint_log.txt")
	logs.LogFlush(logWriter)

	for folder, userSet := range accessMap {
		var emails []string
		for email := range userSet {
			emails = append(emails, email)
		}
		log.Println("📂 Berbagi folder:", folder, "ke", emails)
		err := sharepoint.ShareFolderOnly(folder, emails)
		if err == nil {
			logWriter.WriteString(fmt.Sprintf("[%s] SHARED %s to %v\n", time.Now().Format(time.RFC3339), folder, emails))
			log.Println("📂 Berhasil share folder:", folder, "ke", emails)
		} else {
			logWriter.WriteString(fmt.Sprintf("[%s] ERROR %s: %v\n", time.Now().Format(time.RFC3339), folder, err))
			log.Println("❌ Gagal share folder:", folder, "ke", emails, "-", err)
		}
	}
	fmt.Printf("✅ Total file diekstrak: %d\n", count)
	defer os.RemoveAll(exportFolder) // Hapus folder export setelah selesai
	return nil
}

func GetUserByFolderId(folderId string, db *sql.DB) ([]types.UserFolderAccess, error) {
	query := `
	WITH RECURSIVE item_hierarchy AS (
		SELECT 
			id,
			name,
			owner,
			fullpath AS file_path,
			parent_id,
			id AS root_id,
			name AS root_name,
			0 AS level
		FROM teradocu.folder
		WHERE parent_id IS NULL
		UNION ALL
		SELECT 
			f.id,
			f.name,
			f.owner,
			f.fullpath AS file_path,
			f.parent_id,
			ih.root_id,
			ih.root_name,
			ih.level + 1
		FROM teradocu.folder f
		JOIN item_hierarchy ih ON f.parent_id = ih.id
	)
	SELECT 
		pr.email,
		ih2.id AS folder_id
	FROM item_hierarchy ih2
	JOIN teradocu.folder_profile_role fpr ON fpr.folder_id = ih2.id
	JOIN teradocu.employee_user eu1 ON fpr.user_id = eu1.id
	JOIN teradocu.person pr ON pr.id = eu1.person_id
	WHERE ih2.id = $1
	GROUP BY ih2.id, pr.email;
	`

	rows, err := db.Query(query, folderId)
	if err != nil {
		fmt.Println("❌ Gagal menjalankan query:", err)
		return nil, err
	}
	defer rows.Close()

	listUserFolder := []types.UserFolderAccess{}

	for rows.Next() {
		var email string
		var folderId string
		if err := rows.Scan(&email, &folderId); err != nil {
			fmt.Println("❌ Gagal membaca hasil query:", err)
			continue
		}
		listUserFolder = append(listUserFolder, types.UserFolderAccess{
			Email:    email,
			FolderId: folderId,
		})
	}

	if err := rows.Err(); err != nil {
		fmt.Println("❌ Kesalahan saat membaca hasil query:", err)
		return nil, err
	}

	return listUserFolder, nil
}

func getExtensionFromMime(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "application/pdf":
		return ".pdf"
	case "application/msword":
		return ".doc"
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return ".docx"
	case "application/vnd.ms-excel":
		return ".xls"
	case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return ".xlsx"
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "application/zip":
		return ".zip"
	case "text/plain":
		return ".txt"
	case "text/csv":
		return ".csv"
	case "application/json":
		return ".json"
	default:
		return ".bin"
	}
}

// FileSignature represents a file signature (magic number), extension, and MIME type.
type FileSignature struct {
	Magic     []byte
	Extension string
	Mime      string
}

var knownSignatures = []FileSignature{
	// PDF
	{[]byte{0x25, 0x50, 0x44, 0x46}, ".pdf", "application/pdf"},
	// Office Open XML (docx, xlsx, pptx) and ZIP
	{[]byte{0x50, 0x4B, 0x03, 0x04}, ".zip", "application/zip"},
	{[]byte{0x50, 0x4B, 0x03, 0x04}, ".docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
	{[]byte{0x50, 0x4B, 0x03, 0x04}, ".xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
	{[]byte{0x50, 0x4B, 0x03, 0x04}, ".pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
	// MS Office legacy (doc, msi)
	{[]byte{0xD0, 0xCF, 0x11, 0xE0}, ".doc", "application/msword"},
	{[]byte{0xD0, 0xCF, 0x11, 0xE0}, ".msi", "application/x-msi"},
	// EXE
	{[]byte{0x4D, 0x5A}, ".exe", "application/vnd.microsoft.portable-executable"},
	// RAR
	{[]byte{0x52, 0x61, 0x72, 0x21, 0x1A, 0x07, 0x00}, ".rar", "application/x-rar-compressed"},
	// GZIP
	{[]byte{0x1F, 0x8B, 0x08}, ".gz", "application/gzip"},
	// BMP
	{[]byte{0x42, 0x4D}, ".bmp", "image/bmp"},
	// TIFF
	{[]byte{0x49, 0x49, 0x2A, 0x00}, ".tif", "image/tiff"},
	{[]byte{0x4D, 0x4D, 0x00, 0x2A}, ".tif", "image/tiff"},
	// PNG
	{[]byte{0x89, 0x50, 0x4E, 0x47}, ".png", "image/png"},
	// JPEG
	{[]byte{0xFF, 0xD8, 0xFF}, ".jpg", "image/jpeg"},
	// GIF
	{[]byte{0x47, 0x49, 0x46, 0x38, 0x37, 0x61}, ".gif", "image/gif"},
	{[]byte{0x47, 0x49, 0x46, 0x38, 0x39, 0x61}, ".gif", "image/gif"},
	// Postscript
	{[]byte{0x25, 0x21}, ".ps", "application/postscript"},
	// RTF
	{[]byte{0x7B, 0x5C, 0x72, 0x74, 0x66}, ".rtf", "application/rtf"},
	// XML
	{[]byte{0x3C, 0x3F, 0x78, 0x6D, 0x6C}, ".xml", "application/xml"},
	// JSON
	{[]byte{0x7B}, ".json", "application/json"},
	// MP3
	{[]byte{0xFF, 0xFB}, ".mp3", "audio/mpeg"},
	{[]byte{0x49, 0x44, 0x33}, ".mp3", "audio/mpeg"},
	// MPEG
	{[]byte{0x00, 0x00, 0x01, 0xBA}, ".mpg", "video/mpeg"},
	{[]byte{0x00, 0x00, 0x01, 0xB3}, ".mpg", "video/mpeg"},
	// MP4
	{[]byte{0x66, 0x74, 0x79, 0x70}, ".mp4", "video/mp4"},
	// AVI
	{[]byte{0x52, 0x49, 0x46, 0x46}, ".avi", "video/x-msvideo"},
	// WASM
	{[]byte{0x00, 0x61, 0x73, 0x6D}, ".wasm", "application/wasm"},
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

func sanitizeFileName(name string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|' {
			return '_'
		}
		return r
	}, name)
}
