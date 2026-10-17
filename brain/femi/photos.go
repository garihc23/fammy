package femi

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"femi/brain/photo"
)

const (
	screenW   = 320
	screenH   = 240
	maxSlides = 5
)

// albums lists the album folders under FAMILY_DIR/photos.
func (b *Brain) albums() []string {
	entries, err := os.ReadDir(filepath.Join(b.cfg.FamilyDir, "photos"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// matchAlbum finds an album case-insensitively: exact, then prefix, then
// either name containing the other ("riya" ⇒ "Riya", "Riya's birthday" ⇒ "Riya").
func matchAlbum(albums []string, query string) string {
	q := strings.ToLower(strings.TrimSpace(query))
	q = strings.NewReplacer("'s", "", "’s", "").Replace(q)
	q = strings.TrimSpace(q)
	if q == "" {
		return ""
	}
	for _, a := range albums {
		if strings.ToLower(a) == q {
			return a
		}
	}
	for _, a := range albums {
		if strings.HasPrefix(strings.ToLower(a), q) {
			return a
		}
	}
	for _, a := range albums {
		if strings.Contains(strings.ToLower(a), q) {
			return a
		}
	}
	best := ""
	for _, a := range albums {
		if strings.Contains(q, strings.ToLower(a)) && len(a) > len(best) {
			best = a
		}
	}
	return best
}

func albumImages(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".jpg", ".jpeg", ".png":
			if !e.IsDir() {
				out = append(out, filepath.Join(dir, e.Name()))
			}
		}
	}
	sort.Strings(out)
	return out
}

// loadCaptions reads captions.txt lines "filename: caption" (keys lowercased).
func loadCaptions(path string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, caption, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(caption) != "" {
			out[strings.ToLower(strings.TrimSpace(name))] = strings.TrimSpace(caption)
		}
	}
	return out
}

// ShowPhotos starts a slideshow of up to 5 photos from an album and returns
// at once with what is being shown.
func (b *Brain) ShowPhotos(album string) (string, error) {
	albums := b.albums()
	if len(albums) == 0 {
		return "There are no photo albums yet.", nil
	}
	name := matchAlbum(albums, album)
	if name == "" {
		return fmt.Sprintf("No album matches %q. Available albums: %s.", album, strings.Join(albums, ", ")), nil
	}
	dir := filepath.Join(b.cfg.FamilyDir, "photos", name)
	files := albumImages(dir)
	if len(files) == 0 {
		return fmt.Sprintf("The %s album has no photos yet.", name), nil
	}
	if len(files) > maxSlides {
		files = files[:maxSlides]
	}
	if !b.cfg.Device.Connected() {
		return "", fmt.Errorf("Femi's screen isn't connected right now, so no photos were shown")
	}
	captions := loadCaptions(filepath.Join(dir, "captions.txt"))
	var known []string
	for _, f := range files {
		if c, ok := captions[strings.ToLower(filepath.Base(f))]; ok {
			known = append(known, c)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	b.mu.Lock()
	if b.slideStop != nil {
		b.slideStop() // a new slideshow replaces the old one
	}
	b.slideStop = cancel
	b.mu.Unlock()
	go b.slideshow(ctx, name, files, captions)

	noun := "photos"
	if len(files) == 1 {
		noun = "photo"
	}
	msg := fmt.Sprintf("Showing %d %s of %s", len(files), noun, name)
	if len(known) > 0 {
		msg += " (captions: " + strings.Join(known, "; ") + ")"
	}
	return msg, nil
}

func (b *Brain) slideshow(ctx context.Context, album string, files []string, captions map[string]string) {
	for i, f := range files {
		if i > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(b.cfg.SlideInterval):
			}
		}
		if ctx.Err() != nil {
			return
		}
		pix, err := photo.Load(f, screenW, screenH)
		if err != nil {
			b.logf("photos: %v", err)
			continue
		}
		caption := captions[strings.ToLower(filepath.Base(f))]
		if caption == "" {
			caption = album
		}
		if err := b.cfg.Device.Image(caption, screenW, screenH, pix); err != nil {
			b.logf("photos: sending %s: %v", filepath.Base(f), err)
			return
		}
		b.logf("photos: showed %s (%q)", filepath.Base(f), caption)
	}
}
