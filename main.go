package main

import (
	"converter_blob/database"
	"converter_blob/logs"
	"converter_blob/sharepoint"
	"converter_blob/types"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

const (
	exportFolder   = "pdf_exports"
	fileUserAccess = "users.json"
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
INNER JOIN teradocu.document doc ON doc.id = doc_bl.document_id
INNER JOIN teradocu.document_metadata doc_meta ON doc.id = doc_meta.document_id
INNER JOIN teradocu.folder fl ON doc.folder_id = fl.id
where fl.fullpath LIKE '%IT DEVELOPMENT%'
ORDER BY doc_bl.document_id, doc_bl.version DESC
limit 10
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

		sharepoint.UploadFile(outputPath, folderPath)

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

	SaveUserFolder(db, timestamp)

	SaveListUsers()

	logWriter := logs.SetLog("sharepoint_log.txt")
	logs.LogFlush(logWriter)

	// Ambil data user-folder dari users.json
	file, err := os.Open(fileUserAccess)
	if err != nil {
		log.Printf("❌ Gagal membuka %s: %v", fileUserAccess, err)
	} else {
		defer file.Close()
		type User struct {
			FolderId    string              `json:"folder_id"`
			FolderPath  string              `json:"folder_path"`
			EmailAccess []types.EmailAccess `json:"email_access"`
		}
		var users []User
		decoder := json.NewDecoder(file)
		if err := decoder.Decode(&users); err != nil {
			log.Printf("❌ Gagal decode %s: %v", fileUserAccess, err)
		} else {
			for _, user := range users {
				var emails []string
				for _, ea := range user.EmailAccess {
					emails = append(emails, ea.Email)
				}
				log.Println("📂 Berbagi folder:", user.FolderPath, "ke", emails)
				_ = sharepoint.ShareFolderOnly(user.FolderPath, user.EmailAccess)

				if err := sharepoint.GetAccessListPermission(user.FolderPath); err != nil {
					log.Printf("❌ Gagal mendapatkan daftar akses folder: %v", err)
				}

				// if err == nil {
				// 	logWriter.WriteString(fmt.Sprintf("[%s] SHARED %s to %v\n", time.Now().Format(time.RFC3339), user.FolderPath, emails))
				// 	log.Println("📂 Berhasil share folder:", user.FolderPath, "ke", emails)
				// } else {
				// 	logWriter.WriteString(fmt.Sprintf("[%s] ERROR %s: %v\n", time.Now().Format(time.RFC3339), user.FolderPath, err))
				// 	log.Println("❌ Gagal share folder:", user.FolderPath, "ke", emails, "-", err)
				// }
			}
		}
	}
	fmt.Printf("✅ Total file diekstrak: %d\n", count)

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

// SaveUserFolder saves user access to JSON file.
// If prefixAdditional is not set (""), it defaults to "pdf_exports/".
func SaveUserFolder(db *sql.DB, prefixAdditional ...string) {
	prefix := ""
	if len(prefixAdditional) > 0 && prefixAdditional[0] != "" {
		prefix = prefixAdditional[0]
	}

	listUserFolder, err := GetUserByProfileId("MMSGI_IT_DEVELOPMENT", db)
	if err != nil {
		log.Fatalf("❌ Gagal mendapatkan akses folder: %v", err)
	}

	logWriterUserAccess := logs.SetLog("user_access.txt")
	defer logs.LogFlush(logWriterUserAccess)

	for _, userAccess := range listUserFolder {
		emailAccess := userAccess.EmailAccess
		folderPath := prefix + userAccess.FolderPath
		fID := userAccess.FolderId

		SaveUser(fID, folderPath, emailAccess)
	}
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

func SaveListUsers() {
	file, err := os.Open(fileUserAccess)
	if err != nil {
		log.Fatalf("❌ Gagal membuka file %s: %v", fileUserAccess, err)
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
	}
	defer outFile.Close()

	encoder := json.NewEncoder(outFile)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(emails); err != nil {
		log.Fatalf("❌ Gagal encode list_user.json: %v", err)
	}

	fmt.Println("✅ list_user.json berhasil dibuat.")
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
