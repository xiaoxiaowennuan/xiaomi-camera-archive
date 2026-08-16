package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mijia-archive/internal/auth"
	"mijia-archive/internal/httpapi"
	"mijia-archive/internal/media"
	"mijia-archive/internal/probe"
	"mijia-archive/internal/scanner"
	"mijia-archive/internal/store"
	"mijia-archive/internal/transcode"
)

type commonConfig struct {
	stateDir, zone, libraryMount, libraryHost, initialName, initialPath *string
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: mijia-archive <scan|serve|set-password> [options]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "scan":
		scan()
	case "serve":
		serve()
	case "set-password":
		setPassword()
	default:
		fmt.Fprintln(os.Stderr, "unknown command")
		os.Exit(2)
	}
}

func setPassword() {
	fs := flag.NewFlagSet("set-password", flag.ExitOnError)
	stateDir := fs.String("state-dir", env("MIJIA_STATE_DIR", ""), "writable state directory")
	username := fs.String("username", "admin", "existing username")
	passwordFile := fs.String("password-file", "", "file containing the new password")
	fs.Parse(os.Args[2:])
	if *stateDir == "" || *passwordFile == "" || !auth.UsernamePattern.MatchString(*username) {
		fs.Usage()
		os.Exit(2)
	}
	databasePath := filepath.Join(*stateDir, "archive.db")
	if _, err := os.Stat(databasePath); err != nil {
		fatal(fmt.Errorf("existing database is required"))
	}
	password, err := os.ReadFile(*passwordFile)
	fatal(err)
	plain := strings.TrimSpace(string(password))
	fatal(auth.ValidatePassword(plain))
	hash, err := auth.HashPassword(plain)
	fatal(err)
	database, err := store.Open(databasePath)
	fatal(err)
	defer database.Close()
	fatal(database.UpdatePassword(context.Background(), *username, hash))
	fmt.Println("password updated; existing sessions invalidated")
}

func common(fs *flag.FlagSet) commonConfig {
	return commonConfig{
		stateDir:     fs.String("state-dir", env("MIJIA_STATE_DIR", ""), "writable state directory"),
		zone:         fs.String("timezone", "Asia/Shanghai", "IANA media timezone"),
		libraryMount: fs.String("media-library-root", env("MIJIA_MEDIA_LIBRARY_ROOT", ""), "read-only media library mount inside the runtime"),
		libraryHost:  fs.String("media-library-host-root", env("MIJIA_MEDIA_LIBRARY_HOST_ROOT", ""), "matching host media library root"),
		initialName:  fs.String("initial-folder-name", env("MIJIA_INITIAL_FOLDER_NAME", "Default archive"), "name used for the migrated initial folder"),
		initialPath:  fs.String("initial-folder-path", env("MIJIA_INITIAL_FOLDER_PATH", ""), "host path used for the migrated initial folder"),
	}
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func (c commonConfig) require(fs *flag.FlagSet) {
	if *c.stateDir == "" || *c.libraryMount == "" || *c.libraryHost == "" || *c.initialPath == "" {
		fs.Usage()
		os.Exit(2)
	}
}

func openConfiguredStore(c commonConfig) (*store.Store, media.Library, *time.Location) {
	loc, err := time.LoadLocation(*c.zone)
	fatal(err)
	fatal(os.MkdirAll(*c.stateDir, 0o700))
	database, err := store.Open(filepath.Join(*c.stateDir, "archive.db"))
	fatal(err)
	library := media.Library{HostRoot: *c.libraryHost, MountRoot: *c.libraryMount}
	mountPath, err := library.IsArchiveFolder(*c.initialPath)
	fatal(err)
	_, err = database.EnsureBootstrapFolder(context.Background(), *c.initialName, filepath.Clean(*c.initialPath), mountPath)
	fatal(err)
	return database, library, loc
}

func scanOne(ctx context.Context, database *store.Store, library media.Library, loc *time.Location, stateDir, ffprobePath string, folder store.FolderRecord) (scanner.Summary, error) {
	mountPath, err := library.IsArchiveFolder(folder.ServerPath)
	if err != nil {
		return scanner.Summary{}, err
	}
	return (&scanner.Scanner{FolderID: folder.ID, MediaRoot: mountPath, Location: loc, Store: database, Prober: probe.FFProbe{Path: ffprobePath}}).Scan(ctx, stateDir)
}

func scan() {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	config := common(fs)
	ffprobePath := fs.String("ffprobe", "ffprobe", "ffprobe executable")
	fs.Parse(os.Args[2:])
	config.require(fs)
	database, library, loc := openConfiguredStore(config)
	defer database.Close()
	folders, err := database.Folders(context.Background())
	fatal(err)
	for _, folder := range folders {
		_ = database.SetFolderScan(context.Background(), folder.ID, "scanning", "")
		summary, err := scanOne(context.Background(), database, library, loc, *config.stateDir, *ffprobePath, folder)
		if err != nil {
			_ = database.SetFolderScan(context.Background(), folder.ID, "failed", "scan failed")
			fmt.Printf("folder=%s status=failed\n", folder.PublicID)
			continue
		}
		_ = database.SetFolderScan(context.Background(), folder.ID, "ready", "")
		fmt.Printf("folder=%s segments=%d events=%d probed=%d reused=%d damaged=%d\n", folder.PublicID, summary.Segments, summary.Events, summary.Probed, summary.Reused, summary.Damaged)
	}
}

func serve() {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	config := common(fs)
	listen := fs.String("listen", "127.0.0.1:8080", "HTTP listen address")
	ffmpegPath := fs.String("ffmpeg", "ffmpeg", "ffmpeg executable")
	ffprobePath := fs.String("ffprobe", "ffprobe", "ffprobe executable")
	webRoot := fs.String("web-dir", "web/dist", "built web application directory")
	cacheMax := fs.Int64("transcode-max-bytes", 20<<30, "maximum compatibility cache size")
	adminPasswordFile := fs.String("bootstrap-admin-password-file", "", "file containing the first admin password")
	secureCookies := fs.Bool("secure-cookies", true, "mark session cookies Secure (requires HTTPS)")
	fs.Parse(os.Args[2:])
	config.require(fs)
	database, library, loc := openConfiguredStore(config)
	defer database.Close()

	hasUsers, err := database.HasUsers(context.Background())
	fatal(err)
	if !hasUsers {
		if *adminPasswordFile == "" {
			fatal(fmt.Errorf("bootstrap admin password file is required on first start"))
		}
		password, err := os.ReadFile(*adminPasswordFile)
		fatal(err)
		plain := strings.TrimSpace(string(password))
		if err := auth.ValidateNewUser("admin", plain, "admin"); err != nil {
			fatal(err)
		}
		hash, err := auth.HashPassword(plain)
		fatal(err)
		fatal(database.EnsureAdmin(context.Background(), "admin", hash))
	}

	tm, err := transcode.New(*config.stateDir, *ffmpegPath, 1, *cacheMax)
	fatal(err)
	authManager := auth.New(database, *secureCookies)
	server := httpapi.Server{WebRoot: *webRoot, Location: loc, Store: database, Transcode: tm, Auth: authManager, Library: library}
	server.ScanFolder = func(folder store.FolderRecord) {
		_, err := scanOne(context.Background(), database, library, loc, *config.stateDir, *ffprobePath, folder)
		if err != nil {
			_ = database.SetFolderScan(context.Background(), folder.ID, "failed", "scan failed")
			return
		}
		_ = database.SetFolderScan(context.Background(), folder.ID, "ready", "")
	}
	fatal(database.RecoverInterruptedScans(context.Background()))
	folders, err := database.Folders(context.Background())
	fatal(err)
	for _, folder := range folders {
		segments, _, countErr := database.Counts(context.Background(), folder.ID)
		if countErr != nil {
			fatal(countErr)
		}
		if folder.ScanStatus == "pending" || folder.ScanStatus == "failed" || segments == 0 {
			if started, beginErr := database.BeginFolderScan(context.Background(), folder.ID); beginErr != nil {
				fatal(beginErr)
			} else if started {
				go server.ScanFolder(folder)
			}
		}
	}
	fmt.Println("listening on", *listen)
	fatal(http.ListenAndServe(*listen, server.Handler()))
}

func fatal(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
