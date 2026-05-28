package media

import "testing"

func TestClassifyStickerFormats(t *testing.T) {
	tests := []struct {
		name string
		in   Attributes
		kind Kind
	}{
		{
			name: "static webp sticker",
			in:   Attributes{MimeType: "image/webp", Sticker: true, Alt: ":)"},
			kind: KindSticker,
		},
		{
			name: "animated tgs sticker",
			in:   Attributes{MimeType: "application/x-tgsticker", Sticker: true, Alt: ":)"},
			kind: KindAnimatedSticker,
		},
		{
			name: "video sticker",
			in:   Attributes{MimeType: "video/webm", Sticker: true, Alt: ":)"},
			kind: KindVideoSticker,
		},
		{
			name: "gif animation",
			in:   Attributes{MimeType: "video/mp4", Animated: true},
			kind: KindGIFAnimation,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.in)
			if got.Kind != string(tt.kind) {
				t.Fatalf("kind = %q, want %q", got.Kind, tt.kind)
			}
		})
	}
}

func TestCacheNameSanitizes(t *testing.T) {
	got := CacheName("chat/1", "../sticker.webp", "image/webp")
	if got != "chat_1-sticker.webp" {
		t.Fatalf("cache name = %q", got)
	}
}
