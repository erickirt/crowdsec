package cwhub

import (
	"bytes"
	"context"
	"crypto"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/crowdsecurity/go-cs-lib/downloader"
)

// writeEmbeddedContentTo writes the embedded content to the specified path and checks the hash.
// If the content is base64 encoded, it will be decoded before writing. Call this method only
// if item.Content if not empty.
func (i *Item) writeEmbeddedContentTo(destPath, wantHash string) error {
	if i.Content == "" {
		return fmt.Errorf("no embedded content for %s", i.Name)
	}

	content, err := base64.StdEncoding.DecodeString(i.Content)
	if err != nil {
		content = []byte(i.Content)
	}

	dir := filepath.Dir(destPath)
	reader := bytes.NewReader(content)
	hash := crypto.SHA256.New()

	tee := io.TeeReader(reader, hash)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("while creating %s: %w", dir, err)
	}

	f, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}

	defer f.Close()

	if _, err := io.Copy(f, tee); err != nil {
		return err
	}

	gotHash := hex.EncodeToString(hash.Sum(nil))
	if gotHash != wantHash {
		return fmt.Errorf("%w. The index file is invalid, please run 'cscli hub update' and try again",
			downloader.HashMismatchError{
				Expected: wantHash,
				Got: gotHash,
			})
	}

	return nil
}

// FetchContentTo writes the last version of the item's YAML file to the specified path.
// If the file is embedded in the index file, it will be written directly without downloads.
// Returns whether the file was downloaded (to inform if the security engine needs reloading)
// and the remote url for feedback purposes.
func (i *Item) FetchContentTo(ctx context.Context, contentProvider ContentProvider, destPath string) (bool, string, error) {
	wantHash := i.latestHash()
	if wantHash == "" {
		return false, "", fmt.Errorf("%s: latest hash missing from index. The index file is invalid, please run 'cscli hub update' and try again", i.FQName())
	}

	// Use the embedded content if available
	if i.Content != "" {
		if err := i.writeEmbeddedContentTo(destPath, wantHash); err != nil {
			return false, "", err
		}

		i.State.DownloadPath = destPath

		return true, fmt.Sprintf("(embedded in %s)", i.hub.local.HubIndexFile), nil
	}

	downloaded, _, err := contentProvider.FetchContent(ctx, i.RemotePath, destPath, wantHash, i.hub.logger)

	if err == nil && downloaded {
		i.State.DownloadPath = destPath
	}

	return downloaded, destPath, err
}
