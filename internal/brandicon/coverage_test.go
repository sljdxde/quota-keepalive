package brandicon

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

func TestCoverageHelpers(t *testing.T) {
	if len(MasterPNG()) == 0 {
		t.Fatal("MasterPNG should return the embedded application icon")
	}

	img, err := png.Decode(bytes.NewReader(Application(0)))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 1 || img.Bounds().Dy() != 1 {
		t.Fatalf("non-positive sizes should clamp to 1x1, got %v", img.Bounds())
	}
	if len(Application(1024)) == 0 {
		t.Fatal("an icon already at the requested size should still be encoded")
	}
}

func TestCoverageInvalidImages(t *testing.T) {
	t.Run("decode panic", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("invalid embedded image data should panic")
			}
		}()
		decodeRGBA([]byte("not a PNG"))
	})

	t.Run("encode panic", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("an empty image should fail PNG encoding")
			}
		}()
		encodePNG(image.NewRGBA(image.Rect(0, 0, 0, 0)))
	})
}
