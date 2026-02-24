package main

import (
	"converter_blob/sharepoint"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/schollz/progressbar/v3"
)

type Config struct {
	SourcePath   string
	SPRoot       string
	Worker       int
	ClientID     string
	ClientSecret string
	TenantID     string
	SiteID       string
	DriveID      string
}

type FileJob struct {
	LocalPath string
	SPPath    string
	SizeMB    float64
}

var cfg Config

var (
	timestamp = time.Now().Format("20060102_150405")
)

var once sync.Once

func InitFromEnv() error {

	var err error

	once.Do(func() {
		err = LoadConfigFromEnv()
	})

	return err
}

// ================= CONFIG =================

func loadConfig() Config {

	src := os.Getenv("NAS_PATH")
	if src == "" {
		src = "./data"
	}

	sp := os.Getenv("SP_ROOT")
	if sp == "" {
		sp = "Documents/Migration"
	}

	worker := runtime.NumCPU()

	if w := os.Getenv("WORKER"); w != "" {
		fmt.Sscanf(w, "%d", &worker)
	}

	return Config{
		SourcePath:   src,
		SPRoot:       sp,
		Worker:       worker,
		ClientID:     os.Getenv("MS_CLIENT_ID"),
		ClientSecret: os.Getenv("MS_CLIENT_SECRET"),
		TenantID:     os.Getenv("MS_TENANT_ID"),
		SiteID:       os.Getenv("MS_SITE_ID"),
		DriveID:      os.Getenv("MS_DRIVE_ID"),
	}
}

func LoadConfigFromEnv() error {

	cfg = Config{
		ClientID:     os.Getenv("MS_CLIENT_ID"),
		ClientSecret: os.Getenv("MS_CLIENT_SECRET"),
		TenantID:     os.Getenv("MS_TENANT_ID"),
		SiteID:       os.Getenv("MS_SITE_ID"),
		DriveID:      os.Getenv("MS_DRIVE_ID"),
	}

	if cfg.ClientID == "" ||
		cfg.ClientSecret == "" ||
		cfg.TenantID == "" ||
		cfg.SiteID == "" ||
		cfg.DriveID == "" {

		return fmt.Errorf("sharepoint env config not complete")
	}

	return nil
}

// ================= SCAN =================

func scanFiles(cfg Config, jobs chan<- FileJob) error {

	root := cfg.SourcePath

	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {

		if err != nil {
			log.Printf("❌ Access error: %s (%v)", path, err)
			return nil
		}

		if d.IsDir() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		if info.Size() == 0 {
			return nil
		}

		sizeMB := float64(info.Size()) / (1024 * 1024)

		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)

		spPath := fmt.Sprintf("%s/%s/%s",
			cfg.SPRoot,
			timestamp,
			rel,
		)

		jobs <- FileJob{
			LocalPath: path,
			SPPath:    spPath,
			SizeMB:    sizeMB,
		}

		return nil
	})
}

// ================= RETRY =================

func uploadWithRetry(job FileJob, maxRetry int) error {

	var err error

	for i := 0; i < maxRetry; i++ {

		_, err = sharepoint.UploadFileChunkedResumeV2(
			job.LocalPath,
			job.SPPath,
		)

		if err == nil {
			return nil
		}

		// file exists
		if strings.Contains(err.Error(), "409") {
			return err
		}

		wait := time.Duration(i+1) * 2 * time.Second
		log.Printf("🔄 Retry %d: %s", i+1, job.LocalPath)

		time.Sleep(wait)
	}

	return err
}

// ================= WORKER =================

func worker(
	id int,
	jobs <-chan FileJob,
	bar *progressbar.ProgressBar,
	stats *Stats,
	wg *sync.WaitGroup,
) {

	defer wg.Done()

	for job := range jobs {

		err := uploadWithRetry(job, 3)

		if err != nil {

			stats.mu.Lock()

			if strings.Contains(err.Error(), "409") {
				stats.exists++
			} else {
				stats.failed++
				stats.failedList = append(stats.failedList, job.LocalPath)
			}

			stats.mu.Unlock()

		} else {

			atomic.AddInt64(&stats.success, 1)

			log.Printf("✔️ %s (%.2f MB)",
				filepath.Base(job.LocalPath),
				job.SizeMB,
			)
		}

		bar.Add(1)
	}
}

// ================= STATS =================

type Stats struct {
	success    int64
	failed     int64
	exists     int64
	failedList []string
	mu         sync.Mutex
}

// ================= MAIN PROCESS =================

func run() error {

	start := time.Now()

	cfg := loadConfig()

	// ===== log =====

	_ = os.MkdirAll("logs", os.ModePerm)

	logFile, _ := os.Create(
		"logs/upload_" + timestamp + ".log",
	)

	defer logFile.Close()

	log.SetOutput(io.MultiWriter(os.Stdout, logFile))
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	log.Println("Source :", cfg.SourcePath)
	log.Println("SPRoot :", cfg.SPRoot)
	log.Println("Worker :", cfg.Worker)

	// ===== channels =====

	jobs := make(chan FileJob, 1000)

	stats := &Stats{}

	// ===== scan counter =====

	var total int64

	go func() {

		filepath.WalkDir(cfg.SourcePath, func(path string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				total++
			}
			return nil
		})

	}()

	// wait scan count small delay
	time.Sleep(2 * time.Second)

	bar := progressbar.Default(total, "Uploading")

	// ===== workers =====

	var wg sync.WaitGroup

	for i := 0; i < cfg.Worker; i++ {

		wg.Add(1)

		go worker(
			i,
			jobs,
			bar,
			stats,
			&wg,
		)
	}

	// ===== scan files =====

	err := scanFiles(cfg, jobs)
	if err != nil {
		return err
	}

	close(jobs)

	wg.Wait()

	bar.Finish()

	// ===== summary =====

	if len(stats.failedList) > 0 {

		os.WriteFile(
			"failed.txt",
			[]byte(strings.Join(stats.failedList, "\n")),
			0644,
		)
	}

	log.Println("================================")
	log.Println("DONE")
	log.Println("================================")

	log.Println("Success :", stats.success)
	log.Println("Failed  :", stats.failed)
	log.Println("Exists  :", stats.exists)
	log.Println("Time    :", time.Since(start))

	return nil
}

// ================= MAIN =================

func main() {

	if err := run(); err != nil {
		log.Fatal(err)
	}
}
