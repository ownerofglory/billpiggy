package imageproc_test

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/ownerofglory/billpiggy/pkg/imageproc"
)

// colourPNG builds a PNG of the given size with a recognisable colour gradient.
func colourPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	source := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			source.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 200, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	return encoded.Bytes()
}

func TestNormalizeDownscalesToTheBound(t *testing.T) {
	t.Parallel()
	result, err := imageproc.Normalize(bytes.NewReader(colourPNG(t, 2000, 1000)), imageproc.Options{MaxDimension: 500, JPEGQuality: 80})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if result.Width != 500 || result.Height != 250 {
		t.Fatalf("normalised to %dx%d, want 500x250 preserving aspect ratio", result.Width, result.Height)
	}
	if result.ContentType != "image/jpeg" {
		t.Fatalf("content type = %q, want image/jpeg", result.ContentType)
	}
	if _, err := jpeg.Decode(bytes.NewReader(result.Data)); err != nil {
		t.Fatalf("output is not decodable JPEG: %v", err)
	}
}

func TestNormalizeLeavesSmallImagesAtTheirSize(t *testing.T) {
	t.Parallel()
	result, err := imageproc.Normalize(bytes.NewReader(colourPNG(t, 100, 80)), imageproc.Options{MaxDimension: 500})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if result.Width != 100 || result.Height != 80 {
		t.Fatalf("upscaled to %dx%d; images within the bound must be left alone", result.Width, result.Height)
	}
}

func TestNormalizeGrayscaleRemovesColour(t *testing.T) {
	t.Parallel()
	result, err := imageproc.Normalize(bytes.NewReader(colourPNG(t, 64, 64)), imageproc.ReceiptOptions())
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(result.Data))
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}
	// JPEG chroma subsampling means grayscale is not bit-exact, so allow a
	// small tolerance rather than demanding R == G == B.
	bounds := decoded.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y += 8 {
		for x := bounds.Min.X; x < bounds.Max.X; x += 8 {
			r, g, b, _ := decoded.At(x, y).RGBA()
			if absDiff(r, g) > 0x0808 || absDiff(g, b) > 0x0808 {
				t.Fatalf("pixel (%d,%d) retained colour: r=%d g=%d b=%d", x, y, r, g, b)
			}
		}
	}
}

// photoJPEG builds a noisy JPEG standing in for a phone camera photo. A smooth
// synthetic gradient would compress unrealistically well and make any size
// comparison meaningless.
func photoJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	source := image.NewRGBA(image.Rect(0, 0, width, height))
	seed := uint32(12345)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			seed = seed*1664525 + 1013904223
			noise := uint8(seed >> 24)
			source.Set(x, y, color.RGBA{R: noise, G: noise/2 + 60, B: uint8(x % 251), A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, source, &jpeg.Options{Quality: 92}); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	return encoded.Bytes()
}

func TestNormalizeShrinksReceipts(t *testing.T) {
	t.Parallel()
	original := photoJPEG(t, 2400, 1800)
	result, err := imageproc.Normalize(bytes.NewReader(original), imageproc.ReceiptOptions())
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	// The whole point of normalising receipts is to cut storage and, later,
	// vision-model tokens.
	if len(result.Data) >= len(original) {
		t.Fatalf("normalised receipt is %d bytes, original was %d", len(result.Data), len(original))
	}
	if result.Width != 1600 {
		t.Fatalf("receipt width = %d, want the 1600px bound", result.Width)
	}
}

func TestNormalizeRejectsNonImages(t *testing.T) {
	t.Parallel()
	_, err := imageproc.Normalize(bytes.NewReader([]byte("#!/bin/sh\nrm -rf /\n")), imageproc.ReceiptOptions())
	if !errors.Is(err, imageproc.ErrUnsupportedType) {
		t.Fatalf("Normalize on a shell script returned %v, want ErrUnsupportedType", err)
	}
}

func TestVerifyDeclaredTypeCatchesMismatch(t *testing.T) {
	t.Parallel()
	// A client claiming image/png while uploading a script must be rejected.
	err := imageproc.VerifyDeclaredType([]byte("#!/bin/sh\necho hello\n"), "image/png")
	if !errors.Is(err, imageproc.ErrContentMismatch) {
		t.Fatalf("VerifyDeclaredType returned %v, want ErrContentMismatch", err)
	}
}

func TestVerifyDeclaredTypeAcceptsMatchingContent(t *testing.T) {
	t.Parallel()
	if err := imageproc.VerifyDeclaredType(colourPNG(t, 8, 8), "image/png"); err != nil {
		t.Fatalf("matching PNG rejected: %v", err)
	}
	// Parameters such as charset must not defeat the comparison.
	if err := imageproc.VerifyDeclaredType(colourPNG(t, 8, 8), "image/png; charset=binary"); err != nil {
		t.Fatalf("PNG with a media-type parameter rejected: %v", err)
	}
}

func TestIsSupportedImage(t *testing.T) {
	t.Parallel()
	for mediaType, want := range map[string]bool{
		"image/jpeg":       true,
		"image/png":        true,
		"image/gif":        true,
		"IMAGE/PNG":        true,
		"image/svg+xml":    false,
		"application/pdf":  false,
		"text/html":        false,
		"application/json": false,
	} {
		if got := imageproc.IsSupportedImage(mediaType); got != want {
			t.Fatalf("IsSupportedImage(%q) = %v, want %v", mediaType, got, want)
		}
	}
}

func TestNormalizeStripsMetadata(t *testing.T) {
	t.Parallel()
	// Build a PNG carrying a textual metadata chunk, then confirm the marker
	// does not survive re-encoding. Uploads must not serve back EXIF GPS data.
	original := colourPNG(t, 32, 32)
	marker := []byte("SECRET-GPS-LOCATION")
	withMetadata := append(append([]byte(nil), original...), marker...)

	result, err := imageproc.Normalize(bytes.NewReader(withMetadata), imageproc.ReceiptOptions())
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if bytes.Contains(result.Data, marker) {
		t.Fatal("appended metadata survived normalisation")
	}
}

func absDiff(a, b uint32) uint32 {
	if a > b {
		return a - b
	}
	return b - a
}
