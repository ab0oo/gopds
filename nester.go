package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/ab0oo/gopds/internal/scanner"
)

func main() {
	// 1. Check for command line argument
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run nester.go <directory_path>")
		fmt.Println("Example: go run nester.go \"/share/Multimedia/Books/Anne McCaffrey\"")
		return
	}

	targetDir := os.Args[1]

	// 2. Validate directory
	absPath, err := filepath.Abs(targetDir)
	if err != nil {
		log.Fatalf("❌ Invalid path: %v", err)
	}

	info, err := os.Stat(absPath)
	if err != nil || !info.IsDir() {
		log.Fatalf("❌ Path does not exist or is not a directory: %s", absPath)
	}

	entries, err := os.ReadDir(absPath)
	if err != nil {
		log.Fatalf("❌ Failed to read directory: %v", err)
	}

	fmt.Printf("📂 Processing: %s\n", absPath)

	for _, entry := range entries {
		// Only process files, skip sub-directories already nested
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".epub") {
			continue
		}

		oldPath := filepath.Join(absPath, entry.Name())

		// 3. Extract metadata using your perfected internal/scanner logic
		meta, err := scanner.ExtractMetadata(oldPath)
		if err != nil || meta == nil || meta.Title == "" {
			fmt.Printf("⚠️  Skipping %s: Could not read metadata\n", entry.Name())
			continue
		}

		// 4. Sanitize the title for the filesystem
		// Removes characters that are illegal in Linux/Windows paths
		safeTitle := strings.Map(func(r rune) rune {
			if strings.ContainsRune(`<>:"/\|?*`, r) {
				return '-'
			}
			return r
		}, meta.Title)
		safeTitle = strings.TrimSpace(safeTitle)

		newDir := filepath.Join(absPath, safeTitle)

		// 5. Create the subfolder
		if err := os.MkdirAll(newDir, 0755); err != nil {
			fmt.Printf("❌ Error creating directory %s: %v\n", safeTitle, err)
			continue
		}

		// 6. Move and optionally rename the file to something human-readable
		// Using the safe title for the filename too fixes those MD5 names
		extension := filepath.Ext(entry.Name())
		newFileName := fmt.Sprintf("%s%s", safeTitle, extension)
		newPath := filepath.Join(newDir, newFileName)

		if err := os.Rename(oldPath, newPath); err != nil {
			fmt.Printf("❌ Error moving %s: %v\n", entry.Name(), err)
		} else {
			fmt.Printf("✅ Success: %s -> %s/%s\n", entry.Name(), safeTitle, newFileName)
		}
	}
	fmt.Println("\n✨ Done! Don't forget to delete the old shared cover.jpg before rescanning.")
}
