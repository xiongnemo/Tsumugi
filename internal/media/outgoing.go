package media

import (
	"bufio"
	"context"
	"fmt"
	"image/gif"
	"image/jpeg"
	"image/png"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// PhotoMaxBytes is Telegram's limit for something sent as a *photo*. A bigger image is still
	// sendable, just as a document, which is also the only way to keep the original bytes.
	PhotoMaxBytes = 10 << 20
	// FileMaxBytes is the non-premium upload limit. Checked before the upload starts: finding out
	// after twenty minutes of uploading is not an error message, it is an insult.
	FileMaxBytes = 2 << 30
)

// Outgoing is a local file described the way the sender needs it.
//
// Kind uses the same vocabulary as received media (see Kind constants and the classifier in
// internal/telegram), so the pending row Tsumugi shows before the upload finishes renders exactly
// like the message that replaces it.
type Outgoing struct {
	Path     string
	FileName string
	Size     int64
	Kind     string
	MimeType string
	// AsPhoto sends it as a photo: Telegram re-encodes and strips the original file. False means
	// an uploaded document, which is what preserves the bytes.
	AsPhoto bool
}

// InspectOutgoing describes a local file for sending, or explains why it cannot be sent.
//
// asFile forces the document path for something that would otherwise go as a photo. That choice
// belongs to the user: a photo is smaller and shows inline, a document keeps the original pixels,
// and no default is right for both a screenshot and a scan.
func InspectOutgoing(path string, asFile bool) (Outgoing, error) {
	if strings.TrimSpace(path) == "" {
		return Outgoing{}, fmt.Errorf("no file selected")
	}
	info, err := os.Stat(path)
	if err != nil {
		return Outgoing{}, err
	}
	if info.IsDir() {
		return Outgoing{}, fmt.Errorf("%s is a directory", filepath.Base(path))
	}
	if info.Size() == 0 {
		// Telegram rejects these with an unhelpful error, and an empty file is almost always a
		// half-written download rather than something anyone meant to send.
		return Outgoing{}, fmt.Errorf("%s is empty", filepath.Base(path))
	}
	if info.Size() > FileMaxBytes {
		return Outgoing{}, fmt.Errorf("%s is larger than the %d GB upload limit", filepath.Base(path), FileMaxBytes>>30)
	}

	out := Outgoing{
		Path:     path,
		FileName: filepath.Base(path),
		Size:     info.Size(),
		MimeType: mimeTypeForPath(path),
	}
	out.Kind, out.AsPhoto = outgoingKind(out.MimeType, out.Size, asFile)
	return out, nil
}

// outgoingKind decides what a file is sent as.
//
// GIFs are deliberately not photos: Telegram treats an animation as a document carrying the
// animated attribute, and sending one as a photo would silently flatten it to a still frame.
func outgoingKind(mimeType string, size int64, asFile bool) (kind string, asPhoto bool) {
	switch {
	case asFile:
		return string(KindDocument), false
	case mimeType == "image/gif":
		return string(KindGIFAnimation), false
	case strings.HasPrefix(mimeType, "image/"):
		if size > PhotoMaxBytes {
			// Too big for a photo, but there is no reason to refuse it: as a document it goes
			// through untouched.
			return string(KindDocument), false
		}
		return string(KindPhoto), true
	case strings.HasPrefix(mimeType, "video/"):
		return string(KindVideo), false
	case strings.HasPrefix(mimeType, "audio/"):
		return "audio", false
	default:
		return string(KindDocument), false
	}
}

// mimeTypeForPath resolves a MIME type from the extension.
//
// An explicit table comes first because mime.TypeByExtension reads the Windows registry, where the
// answer depends on what the user has installed — .jpg can come back as "image/pjpeg", and a
// missing entry means an image would be sent as an opaque document.
func mimeTypeForPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if known, ok := knownMimeTypes[ext]; ok {
		return known
	}
	if guessed := mime.TypeByExtension(ext); guessed != "" {
		// Strip any "; charset=..." parameter: Telegram wants the bare type.
		if semi := strings.IndexByte(guessed, ';'); semi >= 0 {
			guessed = strings.TrimSpace(guessed[:semi])
		}
		return guessed
	}
	return "application/octet-stream"
}

var knownMimeTypes = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".webp": "image/webp",
	".bmp":  "image/bmp",
	".tif":  "image/tiff",
	".tiff": "image/tiff",
	".mp4":  "video/mp4",
	".mov":  "video/quicktime",
	".mkv":  "video/x-matroska",
	".webm": "video/webm",
	".avi":  "video/x-msvideo",
	".mp3":  "audio/mpeg",
	".m4a":  "audio/mp4",
	".ogg":  "audio/ogg",
	".oga":  "audio/ogg",
	".opus": "audio/opus",
	".flac": "audio/flac",
	".wav":  "audio/wav",
	".pdf":  "application/pdf",
	".txt":  "text/plain",
	".zip":  "application/zip",
}

// ImageDimensions reads an image's size from its header.
//
// DecodeConfig rather than a full decode: the attach picker asks this for every row the cursor
// passes over, and decoding a 20-megapixel photo to print "6000x4000" would stall the UI.
func ImageDimensions(path string) (width, height int, ok bool) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, false
	}
	defer file.Close()
	// The same three formats decodePreviewImage handles, tried in the same order rather than
	// through image.Decode's registry, so the two cannot disagree about what is previewable.
	for _, decode := range []func(*os.File) (int, int, bool){decodePNGConfig, decodeJPEGConfig, decodeGIFConfig} {
		if _, err := file.Seek(0, 0); err != nil {
			return 0, 0, false
		}
		if w, h, ok := decode(file); ok {
			return w, h, true
		}
	}
	return 0, 0, false
}

func decodePNGConfig(file *os.File) (int, int, bool) {
	cfg, err := png.DecodeConfig(file)
	return cfg.Width, cfg.Height, err == nil
}

func decodeJPEGConfig(file *os.File) (int, int, bool) {
	cfg, err := jpeg.DecodeConfig(file)
	return cfg.Width, cfg.Height, err == nil
}

func decodeGIFConfig(file *os.File) (int, int, bool) {
	cfg, err := gif.DecodeConfig(file)
	return cfg.Width, cfg.Height, err == nil
}

// MediaProbe is what ffprobe could tell us about a file. Ok is false when ffprobe is missing or
// could not answer, which callers must treat as "send it as a plain document" rather than as an
// error: ffmpeg is an optional dependency of this project.
type MediaProbe struct {
	Width    int
	Height   int
	Duration float64
	Ok       bool
}

// probeTimeout bounds the child process. ffprobe on a local file is near-instant; a hang here would
// stall a send with no way for the user to tell why.
const probeTimeout = 5 * time.Second

// ProbeMedia reads dimensions and duration with ffprobe.
//
// Attaching a video without them is what makes a recipient's client show a broken player instead of
// an inline video, so this is worth a subprocess — but only when it is there to run.
func ProbeMedia(path string) MediaProbe {
	if !FFprobeAvailable() || path == "" {
		return MediaProbe{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height:format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path,
	)
	out, err := cmd.Output()
	if err != nil {
		return MediaProbe{}
	}
	return parseProbeOutput(string(out))
}

// parseProbeOutput reads ffprobe's keyless output: width, height, then duration, one per line.
//
// Pure so the parsing is testable without ffprobe installed. Audio files have no video stream, so a
// two-line answer (duration only) has to be accepted as well.
func parseProbeOutput(raw string) MediaProbe {
	var numbers []string
	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line == "N/A" {
			continue
		}
		numbers = append(numbers, line)
	}
	probe := MediaProbe{}
	switch len(numbers) {
	case 1:
		// Duration alone: an audio file, or a video whose stream entries were unavailable.
		probe.Duration, _ = strconv.ParseFloat(numbers[0], 64)
	case 3:
		probe.Width, _ = strconv.Atoi(numbers[0])
		probe.Height, _ = strconv.Atoi(numbers[1])
		probe.Duration, _ = strconv.ParseFloat(numbers[2], 64)
	default:
		return MediaProbe{}
	}
	probe.Ok = probe.Duration > 0 || (probe.Width > 0 && probe.Height > 0)
	return probe
}
