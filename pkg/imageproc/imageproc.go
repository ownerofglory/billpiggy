// Package imageproc validates and normalises user-uploaded images.
//
// It uses only the standard library. A higher-quality resampler such as
// golang.org/x/image/draw was considered and rejected: adding it pulls in an
// upgrade of the whole golang.org/x tree, which forces the module past the
// Go 1.24 toolchain the Dockerfile pins. The area-average downscale here is
// well suited to the actual workload anyway, since receipts are photographs
// being reduced by a large factor, where box filtering is close to ideal.
package imageproc

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"strings"
)

// ErrUnsupportedType reports content that is not an image this package handles.
var ErrUnsupportedType = errors.New("unsupported image type")

// ErrContentMismatch reports a declared media type that the bytes contradict.
var ErrContentMismatch = errors.New("declared content type does not match file contents")

// sniffLength is how many leading bytes http.DetectContentType inspects.
const sniffLength = 512

// Options controls how an image is normalised.
type Options struct {
	// MaxDimension bounds the longest edge, preserving aspect ratio. Zero
	// leaves the image at its original size.
	MaxDimension int
	// Grayscale discards colour. Receipts are text on paper, so colour costs
	// storage and, once the image reaches a vision model, tokens.
	Grayscale bool
	// JPEGQuality selects the re-encoding quality between 1 and 100.
	JPEGQuality int
}

// ReceiptOptions returns the normalisation applied to uploaded receipts:
// grayscale, bounded to a size that stays legible for extraction, and
// re-encoded as JPEG.
//
// 2048/85 rather than the previous 1600/72: thermal-printer receipts pack
// many line items into small, often faded print, and inconsistent line-item
// extraction traced back to over-aggressive downscaling and JPEG compression
// destroying exactly the fine detail a vision model needs to read them.
func ReceiptOptions() Options {
	return Options{MaxDimension: 2048, Grayscale: true, JPEGQuality: 85}
}

// ProfileImageOptions returns the normalisation applied to profile images,
// which keep their colour but are bounded to a display-appropriate size.
func ProfileImageOptions() Options {
	return Options{MaxDimension: 512, Grayscale: false, JPEGQuality: 82}
}

// Result is a normalised image ready to store.
type Result struct {
	// Data is the re-encoded image.
	Data []byte
	// ContentType is the media type of Data.
	ContentType string
	// Width and Height are the normalised dimensions in pixels.
	Width, Height int
}

// DetectContentType reports the media type implied by the leading bytes of an
// upload, ignoring whatever the client declared.
func DetectContentType(head []byte) string {
	if len(head) > sniffLength {
		head = head[:sniffLength]
	}
	return http.DetectContentType(head)
}

// VerifyDeclaredType reports whether the bytes agree with the declared media
// type, so a client cannot smuggle a script past an "image/png" content type.
//
// Only the type and subtype are compared; parameters such as charset differ
// harmlessly between what a browser sends and what sniffing produces.
func VerifyDeclaredType(head []byte, declared string) error {
	detected := DetectContentType(head)
	if baseMediaType(detected) != baseMediaType(declared) {
		return fmt.Errorf("%w: declared %q, detected %q", ErrContentMismatch, declared, detected)
	}
	return nil
}

// IsSupportedImage reports whether a media type is one this package can decode.
func IsSupportedImage(mediaType string) bool {
	switch baseMediaType(mediaType) {
	case "image/jpeg", "image/png", "image/gif":
		return true
	default:
		return false
	}
}

// Normalize decodes, optionally desaturates and downscales an image, and
// re-encodes it as JPEG.
//
// Re-encoding is itself a safety measure: the output is built from decoded
// pixels, so any metadata the original carried — EXIF, GPS coordinates, colour
// profiles, appended payloads — is discarded rather than stored and served back.
func Normalize(reader io.Reader, options Options) (Result, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return Result{}, fmt.Errorf("read image: %w", err)
	}
	if !IsSupportedImage(DetectContentType(data)) {
		return Result{}, ErrUnsupportedType
	}
	source, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrUnsupportedType, err)
	}
	normalised := resize(source, options.MaxDimension)
	if options.Grayscale {
		normalised = grayscale(normalised)
	}
	quality := options.JPEGQuality
	if quality < 1 || quality > 100 {
		quality = 80
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, normalised, &jpeg.Options{Quality: quality}); err != nil {
		return Result{}, fmt.Errorf("encode image: %w", err)
	}
	bounds := normalised.Bounds()
	return Result{
		Data:        encoded.Bytes(),
		ContentType: "image/jpeg",
		Width:       bounds.Dx(),
		Height:      bounds.Dy(),
	}, nil
}

// resize scales an image so its longest edge is at most maxDimension, using an
// area average over each source block. Images already within the bound are
// returned unchanged.
func resize(source image.Image, maxDimension int) image.Image {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if maxDimension <= 0 || (width <= maxDimension && height <= maxDimension) {
		return source
	}
	targetWidth, targetHeight := width, height
	if width >= height {
		targetWidth = maxDimension
		targetHeight = max(1, height*maxDimension/width)
	} else {
		targetHeight = maxDimension
		targetWidth = max(1, width*maxDimension/height)
	}
	target := image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	for y := 0; y < targetHeight; y++ {
		sourceTop := bounds.Min.Y + y*height/targetHeight
		sourceBottom := bounds.Min.Y + (y+1)*height/targetHeight
		if sourceBottom <= sourceTop {
			sourceBottom = sourceTop + 1
		}
		for x := 0; x < targetWidth; x++ {
			sourceLeft := bounds.Min.X + x*width/targetWidth
			sourceRight := bounds.Min.X + (x+1)*width/targetWidth
			if sourceRight <= sourceLeft {
				sourceRight = sourceLeft + 1
			}
			target.Set(x, y, averageColor(source, sourceLeft, sourceTop, sourceRight, sourceBottom))
		}
	}
	return target
}

// averageColor averages the source pixels covering one target pixel.
func averageColor(source image.Image, left, top, right, bottom int) color.RGBA {
	var totalR, totalG, totalB, totalA, samples uint64
	for y := top; y < bottom; y++ {
		for x := left; x < right; x++ {
			r, g, b, a := source.At(x, y).RGBA()
			totalR += uint64(r)
			totalG += uint64(g)
			totalB += uint64(b)
			totalA += uint64(a)
			samples++
		}
	}
	if samples == 0 {
		return color.RGBA{}
	}
	// RGBA() returns 16-bit values; shifting by 8 brings them back to 8-bit.
	return color.RGBA{
		R: uint8(totalR / samples >> 8),
		G: uint8(totalG / samples >> 8),
		B: uint8(totalB / samples >> 8),
		A: uint8(totalA / samples >> 8),
	}
}

// grayscale converts an image to 8-bit luminance.
func grayscale(source image.Image) image.Image {
	bounds := source.Bounds()
	target := image.NewGray(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(target, target.Bounds(), source, bounds.Min, draw.Src)
	return target
}

// baseMediaType strips parameters and normalises case.
func baseMediaType(mediaType string) string {
	if index := strings.IndexByte(mediaType, ';'); index >= 0 {
		mediaType = mediaType[:index]
	}
	return strings.ToLower(strings.TrimSpace(mediaType))
}

// Ensure the decoders for every supported type are registered.
var (
	_ = png.Encode
	_ = gif.Decode
)
