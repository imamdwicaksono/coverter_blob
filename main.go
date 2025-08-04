package main

import (
	"bufio"
	"context"
	"converter_blob/database"
	"converter_blob/logs"
	"converter_blob/sharepoint"
	"converter_blob/types"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	"github.com/schollz/progressbar/v3"
)

const (
	exportFolder   = "pdf_exports"
	fileUserAccess = "users.json"
	// Version        = "1.0.0"
	// BuildDate      = "2024-06-09T00:00:00Z" // Update this with your build date
)

func printVersion() {
	fmt.Printf("📦 Versi: %s\n🕒 Dibangun: %s\n", Version, BuildDate)
}

func main() {
	env := flag.String("env", "", "Environment: dev, prod, atau kosong (default .env)")
	flag.StringVar(env, "e", "", "Alias untuk --env")
	singleFile := flag.String("file", "", "Path ke file PDF untuk diupload")
	folderPath := flag.String("folder", "", "Path ke folder PDF untuk diupload")
	extractFlag := flag.Bool("extract", false, "Ekstrak semua file dari DB ke folder")
	versionFlag := flag.Bool("version", false, "Tampilkan versi aplikasi")
	startFlag := flag.Int("start", 0, "start dari offset tertentu (default 0)")
	endFlag := flag.Int("end", 0, "end pada offset tertentu (default 0)")
	withUploadSharepointFlag := flag.Bool("with-upload-sp", false, "Sertakan upload ke SharePoint")
	noReplace := flag.Bool("no-replace", false, "Jangan timpa file yang sudah ada")

	// Ensure data directory exists
	if _, err := os.Stat("data"); os.IsNotExist(err) {
		os.Mkdir("data", 0755)
	}

	// Check if .env exists, copy from .env.example if not
	if _, err := os.Stat("data/.env"); os.IsNotExist(err) {
		if _, err := os.Stat(".env.example"); !os.IsNotExist(err) {
			src, _ := os.ReadFile(".env.example")
			os.WriteFile("data/.env", src, 0644)
		}
	}

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
		fmt.Println("   --version        Tampilkan versi aplikasi")
		fmt.Println("   --no-replace     Jangan timpa file yang sudah ada")
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
		start := 0
		end := 0 // Default values

		if *startFlag > 0 {
			start = *startFlag
		}
		if *endFlag > 0 {
			end = *endFlag
		}
		if err := extractAllFiles(db, *withUploadSharepointFlag, start, end, *noReplace); err != nil {
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

func loadLargeObject(db *sql.DB, oid uint32) ([]byte, error) {
	conn, err := db.Conn(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to get db conn: %w", err)
	}
	defer conn.Close()

	var data []byte
	row := conn.QueryRowContext(context.Background(), "SELECT lo_get($1)", oid)
	if err := row.Scan(&data); err != nil {
		return nil, fmt.Errorf("failed to read large object: %w", err)
	}
	return data, nil
}

func extractAllFiles(db *sql.DB, withUploadSharepoint bool, start int, end int, noReplace bool) error {

	datetime := time.Now().Format("2006-01-02T15-04-05")
	logPath := "logs/extraction_log_" + datetime + ".txt"
	_ = os.MkdirAll("logs", os.ModePerm)

	logFile, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("failed to create log file: %w", err)
	}
	defer logFile.Close()

	log.SetOutput(io.MultiWriter(os.Stdout, logFile))
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// Validate pagination
	if start != 0 || end != 0 {
		if start < 0 {
			start = 0
		}
		if end <= start {
			end = start + 100
		}
	}

	folder_path := os.Getenv("FOLDER_PATH")
	if folder_path == "" {
		folder_path = "REPOSITORY/MMS GROUP INDONESIA/IT/IT Development"
	}
	query := `
		WITH latest_version AS (
			SELECT document_id, MAX(version) AS version
			FROM teradocu.document_binary_large
			GROUP BY document_id
		)
		SELECT doc_meta.filename, doc_meta.mime_type, doc_meta.file_type,
			fl.fullpath, doc_bl.pdf, doc_bl.binary, fl.id
		FROM teradocu.document_binary_large doc_bl
		JOIN latest_version lv ON lv.document_id = doc_bl.document_id AND lv.version = doc_bl.version
		INNER JOIN teradocu.document doc ON doc.id = doc_bl.document_id
		INNER JOIN teradocu.document_metadata doc_meta ON doc.id = doc_meta.document_id
		INNER JOIN teradocu.folder fl ON doc.folder_id = fl.id
		WHERE fl.fullpath ILIKE '%` + folder_path + `%'
	`

	var rows *sql.Rows
	if end != 0 {
		query += " LIMIT $1 OFFSET $2"
		rows, err = db.Query(query, end-start+1, start)
	} else {
		rows, err = db.Query(query)
	}

	if err != nil {
		return fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	metaFile, err := os.Create("extracted_metadata.csv")
	if err != nil {
		return fmt.Errorf("failed to create metadata CSV: %w", err)
	}
	defer metaFile.Close()
	writer := csv.NewWriter(metaFile)
	defer writer.Flush()
	writer.Write([]string{"file_name", "file_type", "mime_type", "full_path", "saved_path", "size_mb"})

	type extracted struct {
		localPath, sharePointPath string
		sizeMB                    float64
	}

	var (
		extractedFiles  []extracted
		totalSizeMBUint uint64
		count           int32
		startTime       = time.Now()
		mu              sync.Mutex
		timestamp       = time.Now().Format("2006-01-02T15-04-05")
	)

	for rows.Next() {
		var (
			fileName, mimeType, fileType, fullPath string
			pdfData                                []byte
			binaryOid                              sql.NullInt64
			folderId                               string
		)

		if err := rows.Scan(&fileName, &mimeType, &fileType, &fullPath, &pdfData, &binaryOid, &folderId); err != nil {
			log.Printf("❌ Failed to scan row: %v\n", err)
			continue
		}

		var fileData []byte
		if mimeType == "application/pdf" && len(pdfData) > 0 {
			fileData = pdfData
		} else if binaryOid.Valid {
			fileData, err = loadLargeObject(db, uint32(binaryOid.Int64))
			if err != nil {
				log.Printf("❌ Failed to load binary LO %d: %v\n", binaryOid.Int64, err)
				continue
			}
		} else {
			log.Println("⚠️  No valid file content found")
			continue
		}

		sizeMB := float64(len(fileData)) / (1024 * 1024)
		atomic.AddInt32(&count, 1)
		for {
			old := atomic.LoadUint64(&totalSizeMBUint)
			new := math.Float64bits(math.Float64frombits(old) + sizeMB)
			if atomic.CompareAndSwapUint64(&totalSizeMBUint, old, new) {
				break
			}
		}

		safeFolder := filepath.Join(exportFolder, filepath.FromSlash(fullPath))
		_ = os.MkdirAll(safeFolder, os.ModePerm)
		outputName := sanitizeFileName(fileName)
		outputPath := filepath.Join(safeFolder, outputName)

		// Cek jika file sudah ada dan flag noReplace aktif
		if noReplace {
			if _, err := os.Stat(outputPath); err == nil {
				log.Printf("⚠️  Skipping (exists): %s\n", outputPath)
				continue
			}
		}

		if err := os.WriteFile(outputPath, fileData, 0644); err != nil {
			log.Printf("❌ Failed to save file %s: %v\n", outputPath, err)
			continue
		}

		// Optional: auto rename by magic
		actualExt, _ := detectFileType(fileData)
		if currentExt := filepath.Ext(outputPath); actualExt != currentExt && actualExt != "unknown" {
			newPath := strings.TrimSuffix(outputPath, currentExt) + actualExt
			if err := os.Rename(outputPath, newPath); err == nil {
				outputPath = newPath
			}
		}

		spPath := fmt.Sprintf("%s/%s", timestamp, strings.TrimPrefix(outputPath, exportFolder+string(os.PathSeparator)))

		mu.Lock()
		extractedFiles = append(extractedFiles, extracted{outputPath, spPath, sizeMB})
		writer.Write([]string{fileName, fileType, mimeType, fullPath, outputPath, fmt.Sprintf("%.2f", sizeMB)})
		mu.Unlock()

		log.Printf("📄 [%d] %s → %s (%.2f MB)\n", count, fileName, outputPath, sizeMB)
	}

	log.Printf("✅ Extraction completed — %d files, %.2f MB, elapsed: %s\n",
		count,
		math.Float64frombits(atomic.LoadUint64(&totalSizeMBUint)),
		time.Since(startTime),
	)

	log.Printf("\n✅ Extraction completed!\n")
	log.Printf("📂 Total files extracted: %d\n", count)
	log.Printf("📦 Total size extracted: %.2f MB\n", math.Float64frombits(atomic.LoadUint64(&totalSizeMBUint)))
	log.Printf("⏱️  Extraction time: %s\n", time.Since(startTime))

	if withUploadSharepoint {
		fmt.Println("\n🚀 Starting SharePoint upload process...")
		uploadStart := time.Now()
		var uploadCount int32 = 0
		var uploadWg sync.WaitGroup
		uploadSem := make(chan struct{}, 5)
		bar := progressbar.Default(int64(len(extractedFiles)), "Uploading to SharePoint")

		for _, file := range extractedFiles {
			uploadWg.Add(1)
			uploadSem <- struct{}{}

			go func(f extracted) {
				defer uploadWg.Done()
				defer func() { <-uploadSem }()
				defer bar.Add(1)

				_, err := sharepoint.UploadFile(f.localPath, f.sharePointPath)
				if err != nil {
					fmt.Printf("\n❌ Failed to upload %s: %v\n", f.localPath, err)
				} else {
					atomic.AddInt32(&uploadCount, 1)
					fmt.Printf("\n✔️ Uploaded %s (%.2f MB) to %s\n", filepath.Base(f.localPath), f.sizeMB, f.sharePointPath)
				}
			}(file)
		}
		uploadWg.Wait()
		bar.Finish()

		fmt.Printf("\n✅ SharePoint upload completed!\n")
		fmt.Printf("📤 Successfully uploaded %d/%d files\n", uploadCount, len(extractedFiles))
		fmt.Printf("⏱️  Upload time: %s\n", time.Since(uploadStart))
	}

	// logWriter := logs.SetLog("sharepoint_log.txt")
	// logs.LogFlush(logWriter)

	// if err := SaveUserFolder(db, timestamp); err != nil {
	// 	return fmt.Errorf("failed to save folder access: %w", err)
	// }
	// fmt.Println("✅ Successfully saved folder access to users.json")

	// if err := SaveListUsers(); err != nil {
	// 	return fmt.Errorf("failed to save user list: %w", err)
	// }
	// fmt.Println("✅ Successfully saved user list to list_user.json")

	return nil
}

// Helper function for buffered file writing
func writeFileWithBuffer(path string, data []byte) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := bufio.NewWriterSize(file, 32*1024) // 32KB buffer
	if _, err := writer.Write(data); err != nil {
		return err
	}
	return writer.Flush()
}

func extractAllFilesOld(db *sql.DB, withUploadSharepoint bool) error {
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
INNER JOIN teradocu.document doc ON doc.id = doc_bl.document_id
INNER JOIN teradocu.document_metadata doc_meta ON doc.id = doc_meta.document_id
INNER JOIN teradocu.folder fl ON doc.folder_id = fl.id
where fl.fullpath LIKE '%REPOSITORY%'
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
	timestamp := time.Now().Format("2006-01-02T15-04-05") // folder → user set
	startTime := time.Now()

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

		if withUploadSharepoint {
			sharepoint.UploadFile(outputPath, folderPath)
		}

		// folderKey := fmt.Sprintf("%s/%s", timestamp, fullPath)

		// if _, ok := accessMap[folderKey]; !ok {
		// 	accessMap[folderKey] = make(map[string]bool)
		// }
		// accessMap[folderKey]["imam.dwicaksono@mmsgroup.co.id"] = true
		// accessMap[folderKey]["rio.ariandi@mmsgroup.co.id"] = true
		var index = count + 1 // Increment index for next file
		fmt.Printf("📄 Ekstrak file ke %s : %s → %s\n", strconv.Itoa(index), fileName, outputPath)
		count++
	}

	// logWriter := logs.SetLog("sharepoint_log.txt")
	// logs.LogFlush(logWriter)

	// Ambil data user-folder dari users.json
	// file, err := os.Open(fileUserAccess)
	// if err != nil {
	// 	log.Printf("❌ Gagal membuka %s: %v", fileUserAccess, err)
	// } else {
	// 	defer file.Close()
	// 	type User struct {
	// 		FolderId    string              `json:"folder_id"`
	// 		FolderPath  string              `json:"folder_path"`
	// 		EmailAccess []types.EmailAccess `json:"email_access"`
	// 	}
	// 	var users []User
	// 	decoder := json.NewDecoder(file)
	// 	if err := decoder.Decode(&users); err != nil {
	// 		log.Printf("❌ Gagal decode %s: %v", fileUserAccess, err)
	// 	} else {
	// 		for _, user := range users {
	// 			var emails []string
	// 			for _, ea := range user.EmailAccess {
	// 				emails = append(emails, ea.Email)
	// 			}
	// 			log.Println("📂 Berbagi folder:", user.FolderPath, "ke", emails)
	// 			_ = sharepoint.ShareFolderOnly(user.FolderPath, user.EmailAccess)

	// 			// if err := sharepoint.GetAccessListPermission(user.FolderPath); err != nil {
	// 			// 	log.Printf("❌ Gagal mendapatkan daftar akses folder: %v", err)
	// 			// }

	// 			// if err == nil {
	// 			// 	logWriter.WriteString(fmt.Sprintf("[%s] SHARED %s to %v\n", time.Now().Format(time.RFC3339), user.FolderPath, emails))
	// 			// 	log.Println("📂 Berhasil share folder:", user.FolderPath, "ke", emails)
	// 			// } else {
	// 			// 	logWriter.WriteString(fmt.Sprintf("[%s] ERROR %s: %v\n", time.Now().Format(time.RFC3339), user.FolderPath, err))
	// 			// 	log.Println("❌ Gagal share folder:", user.FolderPath, "ke", emails, "-", err)
	// 			// }
	// 		}
	// 	}
	// }
	fmt.Printf("✅ Total file diekstrak: %d\n", count)
	fmt.Printf("Waktu selesai: %s\n", time.Since(startTime))

	// SaveUserFolder(db, timestamp)
	// fmt.Println("✅ Berhasil menyimpan akses folder ke users.json")
	// SaveListUsers()
	// fmt.Println("✅ Berhasil menyimpan daftar user ke list_user.json")

	// check permission folder

	return nil
}

func GetUserByProfileId(profile_id string, db *sql.DB) ([]types.UserFolderAccess, error) {
	rows, err := database.GetUserByProfile(db, profile_id)
	if err != nil {
		fmt.Println("❌ Gagal menjalankan query:", err)
		return nil, err
	}
	defer rows.Close()

	listUserFolder := []types.UserFolderAccess{}

	for rows.Next() {
		var email string
		var profileId string
		var folderId string
		var filePath string
		var folderRole string
		if err := rows.Scan(&email, &profileId, &folderId, &filePath, &folderRole); err != nil {
			fmt.Println("❌ Gagal membaca hasil query:", err)
			continue
		}
		listUserFolder = append(listUserFolder, types.UserFolderAccess{
			EmailAccess: types.EmailAccess{
				Email:          email,
				FolderRole:     folderRole,
				SharepointRole: GetFolderRolePermission(folderRole),
			},
			FolderId:   folderId,
			FolderPath: filePath,
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

	//zip
	{[]byte{0x50, 0x4B, 0x03, 0x04}, ".zip", "application/zip"},
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

// SaveUserFolder saves user access to JSON file.
// If prefixAdditional is not set (""), it defaults to "pdf_exports/".
func SaveUserFolder(db *sql.DB, prefixAdditional ...string) error {
	prefix := ""
	if len(prefixAdditional) > 0 && prefixAdditional[0] != "" {
		prefix = prefixAdditional[0]
	}

	listUserFolder, err := GetUserByProfileId("MMSGI_IT_DEVELOPMENT", db)
	if err != nil {
		log.Fatalf("❌ Gagal mendapatkan akses folder: %v", err)
		return err
	}

	logWriterUserAccess := logs.SetLog("user_access.txt")
	defer logs.LogFlush(logWriterUserAccess)

	for _, userAccess := range listUserFolder {
		emailAccess := userAccess.EmailAccess
		folderPath := prefix + userAccess.FolderPath
		fID := userAccess.FolderId

		if err := SaveUser(fID, folderPath, emailAccess); err != nil {
			log.Printf("❌ Gagal menyimpan akses folder %s: %v", fID, err)
			return err
		}
	}

	return nil
}

func SaveUser(folderId string, folderPath string, email types.EmailAccess) error {

	type User struct {
		FolderId    string              `json:"folder_id"`
		FolderPath  string              `json:"folder_path"`
		EmailAccess []types.EmailAccess `json:"email_access"`
	}

	file, err := os.OpenFile(fileUserAccess, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("gagal membuka file %s: %w", fileUserAccess, err)
	}
	defer file.Close()

	var users []User

	stat, _ := file.Stat()
	if stat.Size() > 0 {
		decoder := json.NewDecoder(file)
		if err := decoder.Decode(&users); err != nil {
			return fmt.Errorf("gagal decode users.json: %w", err)
		}
	}

	found := false
	for i, u := range users {
		if u.FolderId == folderId {
			// Update folder path jika berubah
			users[i].FolderPath = folderPath

			// Tambahkan email jika belum ada
			emailExists := false
			for _, e := range users[i].EmailAccess {
				if e.Email == email.Email {
					emailExists = true
					break
				}
			}
			if !emailExists {
				users[i].EmailAccess = append(users[i].EmailAccess, email)
			}

			found = true
			break
		}
	}
	if !found {
		users = append(users, User{
			FolderId:    folderId,
			FolderPath:  folderPath,
			EmailAccess: []types.EmailAccess{email, {Email: email.Email, FolderRole: "FOLDER_VIEWER", SharepointRole: GetFolderRolePermission("FOLDER_VIEWER")}},
		})
	}

	// Tulis ulang file
	file.Truncate(0)
	file.Seek(0, 0)
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(users); err != nil {
		return fmt.Errorf("gagal encode users.json: %w", err)
	}

	fmt.Printf("✅ Berhasil menyimpan akses folder %s untuk email %s\n", folderId, email)
	return nil

}

func SaveListUsers() error {
	file, err := os.Open(fileUserAccess)
	if err != nil {
		log.Fatalf("❌ Gagal membuka file %s: %v", fileUserAccess, err)
		return err
	}
	defer file.Close()

	type User struct {
		FolderId    string              `json:"folder_id"`
		FolderPath  string              `json:"folder_path"`
		EmailAccess []types.EmailAccess `json:"email_access"`
	}

	var users []User
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&users); err != nil {
		log.Fatalf("❌ Gagal decode %s: %v", fileUserAccess, err)
	}

	// Group unique emails only (ignore role, folder, etc.)
	emailSet := make(map[string]struct{})
	for _, user := range users {
		for _, email := range user.EmailAccess {
			emailSet[email.Email] = struct{}{}
		}
	}

	var emails []string
	for email := range emailSet {
		emails = append(emails, email)
	}

	outFile, err := os.Create("list_user.json")
	if err != nil {
		log.Fatalf("❌ Gagal membuat list_user.json: %v", err)
		return err
	}
	defer outFile.Close()

	encoder := json.NewEncoder(outFile)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(emails); err != nil {
		log.Fatalf("❌ Gagal encode list_user.json: %v", err)
		return err
	}

	fmt.Println("✅ list_user.json berhasil dibuat.")

	return nil
}

// GetFolderRoleAccessByRole returns the FolderRoleAccess with the given role, or nil if not found.
func GetFolderRolePermission(role string) *types.FolderRoleAccess {
	if role == "FOLDER_VIEWER" {
		// You can add any specific logic for ROLE_VIEWER here if needed.
		return &types.FolderRoleAccess{
			FolderRole:     role,
			RolePermission: "read",
		}
	}
	if role == "FOLDER_CONTRIBUTOR" {
		// You can add any specific logic for ROLE_EDITOR here if needed.
		return &types.FolderRoleAccess{
			FolderRole:     role,
			RolePermission: "write",
		}
	}
	if role == "FOLDER_ADMIN" {
		// You can add any specific logic for ROLE_EDITOR here if needed.
		return &types.FolderRoleAccess{
			FolderRole:     role,
			RolePermission: "owner",
		}
	}
	return &types.FolderRoleAccess{
		FolderRole:     role,
		RolePermission: "view", // Default permission
	}
}
