package ui

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func createTestImage(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	// Write a minimal 1x1 PNG (valid header).
	png := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG signature
		0x00, 0x00, 0x00, 0x01, // chunk length
	}
	if err := os.WriteFile(path, png, 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExtractImageAttachments_NoImages(t *testing.T) {
	text := "hello world, no images here"
	clean, att, errs := extractImageAttachments(text)
	if clean != text {
		t.Errorf("expected text unchanged, got %q", clean)
	}
	if len(att) != 0 {
		t.Errorf("expected no attachments, got %d", len(att))
	}
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestExtractImageAttachments_SinglePath(t *testing.T) {
	dir := t.TempDir()
	imgPath := createTestImage(t, dir, "test.png")

	text := imgPath
	clean, att, errs := extractImageAttachments(text)
	if clean != "[Image #1]" {
		t.Errorf("expected '[Image #1]', got %q", clean)
	}
	if len(att) != 1 {
		t.Errorf("expected 1 attachment, got %d", len(att))
	}
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if att[0].MediaType != "image/png" {
		t.Errorf("expected image/png, got %s", att[0].MediaType)
	}
	if att[0].Path != imgPath {
		t.Errorf("expected path %s, got %s", imgPath, att[0].Path)
	}
	// Verify base64 data is valid.
	if _, err := base64.StdEncoding.DecodeString(att[0].Data); err != nil {
		t.Errorf("invalid base64 data: %v", err)
	}
}

func TestExtractImageAttachments_SingleQuoted(t *testing.T) {
	dir := t.TempDir()
	imgPath := createTestImage(t, dir, "photo.jpg")

	text := "look at this '" + imgPath + "' please"
	clean, att, errs := extractImageAttachments(text)
	if clean != "look at this [Image #1] please" {
		t.Errorf("expected 'look at this [Image #1] please', got %q", clean)
	}
	if len(att) != 1 || len(errs) != 0 {
		t.Errorf("att=%d errs=%v", len(att), errs)
	}
}

func TestExtractImageAttachments_DoubleQuoted(t *testing.T) {
	dir := t.TempDir()
	imgPath := createTestImage(t, dir, "photo.jpeg")

	text := `check "` + imgPath + `" out`
	clean, att, errs := extractImageAttachments(text)
	if clean != "check [Image #1] out" {
		t.Errorf("expected 'check [Image #1] out', got %q", clean)
	}
	if len(att) != 1 || len(errs) != 0 {
		t.Errorf("att=%d errs=%v", len(att), errs)
	}
}

func TestExtractImageAttachments_EscapedSpaces(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "my folder")
	os.MkdirAll(subDir, 0755)
	imgPath := filepath.Join(subDir, "test.png")
	createTestImage(t, subDir, "test.png")

	// Simulate terminal escaping: /tmp/xxx/my\ folder/test.png
	escapedPath := filepath.Join(dir, "my\\ folder", "test.png")
	text := escapedPath
	clean, att, errs := extractImageAttachments(text)
	if clean != "[Image #1]" {
		t.Errorf("expected '[Image #1]', got %q", clean)
	}
	if len(att) != 1 {
		t.Errorf("expected 1 attachment, got %d", len(att))
	}
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	_ = imgPath
}

func TestExtractImageAttachments_MultipleEscapedSpaces(t *testing.T) {
	// Real macOS screenshot drag-drop format: Screenshot\ 2026-03-10\ at\ 11.29.23.png
	dir := t.TempDir()
	subDir := filepath.Join(dir, "TemporaryItems")
	os.MkdirAll(subDir, 0755)
	createTestImage(t, subDir, "Screenshot 2026-03-10 at 11.29.23.png")

	// Terminal escapes every space with backslash
	escapedPath := subDir + `/Screenshot\ 2026-03-10\ at\ 11.29.23.png`
	text := escapedPath
	clean, att, errs := extractImageAttachments(text)
	if clean != "[Image #1]" {
		t.Errorf("expected '[Image #1]', got %q", clean)
	}
	if len(att) != 1 {
		t.Errorf("expected 1 attachment, got %d", len(att))
	}
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
}

func TestExtractImageAttachments_MultipleImages(t *testing.T) {
	dir := t.TempDir()
	img1 := createTestImage(t, dir, "a.png")
	img2 := createTestImage(t, dir, "b.gif")

	text := img1 + " and " + img2
	clean, att, errs := extractImageAttachments(text)
	if clean != "[Image #1] and [Image #2]" {
		t.Errorf("expected '[Image #1] and [Image #2]', got %q", clean)
	}
	if len(att) != 2 {
		t.Errorf("expected 2 attachments, got %d", len(att))
	}
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
}

func TestExtractImageAttachments_NonexistentPath(t *testing.T) {
	text := "/nonexistent/path/to/image.png"
	clean, att, errs := extractImageAttachments(text)
	if clean != text {
		t.Errorf("expected text unchanged, got %q", clean)
	}
	if len(att) != 0 {
		t.Errorf("expected no attachments, got %d", len(att))
	}
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
}

func TestExtractImageAttachments_NonImageExtension(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "document.pdf")
	os.WriteFile(path, []byte("fake pdf"), 0644)

	text := path
	clean, att, errs := extractImageAttachments(text)
	if clean != text {
		t.Errorf("expected text unchanged, got %q", clean)
	}
	if len(att) != 0 {
		t.Errorf("expected no attachments, got %d", len(att))
	}
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
}

func TestExtractImageAttachments_MixedTextAndImages(t *testing.T) {
	dir := t.TempDir()
	imgPath := createTestImage(t, dir, "screenshot.webp")

	text := "please check this " + imgPath + " and tell me what you see"
	clean, att, errs := extractImageAttachments(text)
	if clean != "please check this [Image #1] and tell me what you see" {
		t.Errorf("unexpected clean text: %q", clean)
	}
	if len(att) != 1 || len(errs) != 0 {
		t.Errorf("att=%d errs=%v", len(att), errs)
	}
}

func TestExtractImageAttachments_CaseInsensitiveExtension(t *testing.T) {
	dir := t.TempDir()
	imgPath := createTestImage(t, dir, "photo.PNG")

	text := imgPath
	clean, att, errs := extractImageAttachments(text)
	if clean != "[Image #1]" {
		t.Errorf("expected '[Image #1]', got %q", clean)
	}
	if len(att) != 1 || len(errs) != 0 {
		t.Errorf("att=%d errs=%v", len(att), errs)
	}
}

func TestMediaTypeFromExt(t *testing.T) {
	tests := map[string]string{
		".png":  "image/png",
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".gif":  "image/gif",
		".webp": "image/webp",
		".bmp":  "image/bmp",
	}
	for ext, want := range tests {
		got, ok := imageExtensions[ext]
		if !ok {
			t.Errorf("extension %s not found", ext)
			continue
		}
		if got != want {
			t.Errorf("imageExtensions[%s] = %s, want %s", ext, got, want)
		}
	}
}
